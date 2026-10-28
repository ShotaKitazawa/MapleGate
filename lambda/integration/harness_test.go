//go:build integration

// Package integration runs the real Lambda handlers and janitor on the host against DynamoDB Local, and the real
// vpn-agent with WireGuard (userspace) in Apple `container` VMs, standing in for the EC2 node.
package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/config"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/ec2ctl"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/interactions"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/janitor"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/sessionapi"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const (
	ddbName  = "gamevpn-integration-ddb"
	imageTag = "gamevpn-integration"
	ddbLocal = "http://127.0.0.1:8000"
)

var (
	agentDir string
	gateway  string // the host as seen from inside the containers
	ddb      *dynamodb.Client
)

func TestMain(m *testing.M) { os.Exit(setup(m)) }

func setup(m *testing.M) int {
	if _, err := exec.LookPath("container"); err != nil {
		fmt.Fprintln(os.Stderr, "the integration tests need Apple `container` (https://github.com/apple/container)")
		return 1
	}
	_ = exec.Command("container", "rm", "-f", ddbName).Run()
	if out, err := exec.Command("container", "run", "-d", "--name", ddbName, "-p", "127.0.0.1:8000:8000", "amazon/dynamodb-local:latest").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "start DynamoDB Local: %v\n%s", err, out)
		return 1
	}
	defer func() { _ = exec.Command("container", "rm", "-f", ddbName).Run() }()

	info, err := inspect(ddbName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	gateway = info.gateway

	tmp, err := os.MkdirTemp("", "gamevpn-integration")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	agentDir = filepath.Join(tmp, "agent")
	build := exec.Command("go", "build", "-o", filepath.Join(agentDir, "vpn-agent"), "../../node/agent/cmd/vpn-agent")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build agent: %v\n%s", err, out)
		return 1
	}
	if out, err := exec.Command("container", "build", "-t", imageTag, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build image: %v\n%s", err, out)
		return 1
	}

	ddb = dynamodb.New(dynamodb.Options{
		Region: "us-west-2", BaseEndpoint: aws.String(ddbLocal),
		Credentials: credentials.NewStaticCredentialsProvider("x", "x", ""),
	})
	for i := 0; ; i++ {
		if _, err := ddb.ListTables(context.Background(), &dynamodb.ListTablesInput{}); err == nil {
			break
		} else if i > 60 {
			fmt.Fprintln(os.Stderr, "DynamoDB Local did not come up:", err)
			return 1
		}
		time.Sleep(time.Second)
	}
	return m.Run()
}

type containerInfo struct {
	state   string
	ip      string
	gateway string
}

func ctr(args ...string) (string, error) {
	out, err := exec.Command("container", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func inspect(name string) (containerInfo, error) {
	out, err := ctr("inspect", name)
	if err != nil {
		return containerInfo{}, err
	}
	var v []struct {
		Status struct {
			State    string `json:"state"`
			Networks []struct {
				IPv4    string `json:"ipv4Address"`
				Gateway string `json:"ipv4Gateway"`
			} `json:"networks"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || len(v) == 0 {
		return containerInfo{}, fmt.Errorf("inspect %s: %q", name, out)
	}
	ci := containerInfo{state: v[0].Status.State}
	if n := v[0].Status.Networks; len(n) > 0 {
		ci.ip, _, _ = strings.Cut(n[0].IPv4, "/")
		ci.gateway = n[0].Gateway
	}
	return ci, nil
}

// containerEC2 is the janitor's EC2: starting the node means running or restarting its container.
type containerEC2 struct {
	name string
	run  func() error
}

func (c *containerEC2) State(context.Context) (ec2ctl.State, error) {
	ci, err := inspect(c.name)
	if err != nil {
		return ec2ctl.StateStopped, nil // never created yet: the first boot
	}
	if ci.state == "running" {
		return ec2ctl.StateRunning, nil
	}
	return ec2ctl.StateStopped, nil
}

func (c *containerEC2) EnsureStarted(ctx context.Context) error {
	st, _ := c.State(ctx)
	if st != ec2ctl.StateStopped {
		return nil
	}
	if _, err := inspect(c.name); err != nil {
		return c.run()
	}
	_, err := ctr("start", c.name)
	return err
}

// proxy stands between the node and DynamoDB so a test can cut the node's access without touching the Lambdas.
type proxy struct {
	l     net.Listener
	mu    sync.Mutex
	down  bool
	conns []net.Conn
}

func newProxy(t *testing.T) *proxy {
	l, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	p := &proxy{l: l}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go p.serve(c)
		}
	}()
	return p
}

func (p *proxy) port() int { return p.l.Addr().(*net.TCPAddr).Port }

func (p *proxy) serve(c net.Conn) {
	p.mu.Lock()
	if p.down {
		p.mu.Unlock()
		_ = c.Close()
		return
	}
	up, err := net.Dial("tcp", "127.0.0.1:8000")
	if err != nil {
		p.mu.Unlock()
		_ = c.Close()
		return
	}
	p.conns = append(p.conns, c, up)
	p.mu.Unlock()
	go func() { _, _ = io.Copy(up, c); _ = up.Close() }()
	_, _ = io.Copy(c, up)
	_ = c.Close()
}

func (p *proxy) setDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
	if down {
		for _, c := range p.conns {
			_ = c.Close()
		}
		p.conns = nil
	}
}

type nodeOpts struct {
	bootGrace, idleStop string
}

type cluster struct {
	t         *testing.T
	node      string
	st        *store.Dynamo
	api       *sessionapi.Handler
	inter     *interactions.Handler
	jan       *janitor.Janitor
	proxy     *proxy
	serverPub string
	discordSK ed25519.PrivateKey
	ec2       *containerEC2
}

func newCluster(t *testing.T, opts nodeOpts) *cluster {
	ctx := context.Background()
	rnd := make([]byte, 4)
	_, _ = rand.Read(rnd)
	suffix := hex.EncodeToString(rnd)
	table := "gamevpn-integration-" + suffix
	_, err := ddb.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: &table,
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
		},
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("PK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("SK"), AttributeType: types.ScalarAttributeTypeS},
		},
		BillingMode: types.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	c := &cluster{t: t, node: "gamevpn-integration-node-" + suffix, proxy: newProxy(t)}
	t.Cleanup(func() {
		_, _ = ctr("rm", "-f", c.node)
		_, _ = ddb.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: &table})
	})

	serverKey, err := wgtypes.GeneratePrivateKey()
	require.NoError(t, err)
	c.serverPub = serverKey.PublicKey().String()

	imds := httptest.NewUnstartedServer(http.HandlerFunc(c.serveIMDS))
	l, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	imds.Listener = l
	imds.Start()
	t.Cleanup(imds.Close)

	c.ec2 = &containerEC2{name: c.node, run: func() error {
		bootGrace, idleStop := opts.bootGrace, opts.idleStop
		if bootGrace == "" {
			bootGrace = "10m"
		}
		if idleStop == "" {
			idleStop = "15m"
		}
		out, err := ctr("run", "-d", "--name", c.node, "--cap-add", "CAP_NET_ADMIN",
			"-v", agentDir+":/opt/agent:ro",
			"-e", "SERVER_KEY="+serverKey.String(),
			"-e", "TABLE_NAME="+table,
			"-e", "AWS_REGION=us-west-2", "-e", "AWS_ACCESS_KEY_ID=x", "-e", "AWS_SECRET_ACCESS_KEY=x",
			"-e", fmt.Sprintf("AWS_ENDPOINT_URL_DYNAMODB=http://%s:%d", gateway, c.proxy.port()),
			"-e", "AWS_EC2_METADATA_SERVICE_ENDPOINT="+fmt.Sprintf("http://%s:%d", gateway, l.Addr().(*net.TCPAddr).Port),
			"-e", "TICK=2s", "-e", "BOOT_GRACE="+bootGrace, "-e", "IDLE_STOP="+idleStop,
			imageTag, "/usr/local/bin/node-entrypoint.sh")
		if err != nil {
			return fmt.Errorf("run node: %w: %s", err, out)
		}
		return nil
	}}

	c.st = &store.Dynamo{Client: ddb, Table: table}
	cfg := config.Config{
		WGPort: 51820, WGSubnet: "10.66.0", SessionTTL: 4 * time.Hour, PendingTTL: 15 * time.Minute,
		StaleAfter: 2 * time.Minute, AllowedIPs: []string{"0.0.0.0/0"}, Keepalive: 25,
	}
	fd := fakeDiscord{}
	id := 0
	c.api = &sessionapi.Handler{
		Cfg: cfg, Store: c.st, Discord: fd, Auth: sessionapi.IAMAuth{},
		ChannelID:       func(context.Context) (string, error) { return "ch", nil },
		ServerPublicKey: func(context.Context) (string, error) { return c.serverPub, nil },
		Now:             time.Now,
		NewID:           func() string { id++; return fmt.Sprintf("s%04d", id) },
	}
	pub, sk, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	c.discordSK = sk
	c.inter = &interactions.Handler{
		Cfg: cfg, Store: c.st, Discord: fd, Invoker: invoker{c},
		ChannelID: func(context.Context) (string, error) { return "ch", nil },
		PublicKey: func(context.Context) (ed25519.PublicKey, error) { return pub, nil },
		Now:       time.Now,
	}
	c.jan = &janitor.Janitor{
		Cfg: janitor.Config{
			ProvisionTimeout: 90 * time.Second, HandshakeIdle: 10 * time.Minute, StaleAfter: 2 * time.Minute,
			MsgSyncGiveUp: time.Hour, StartCooldown: 15 * time.Minute, BreakerLimit: 2,
			InstanceID: "i-1", UnresponsiveAfter: 10 * time.Minute, OverstayAfter: 35 * time.Minute, AlertRepeat: time.Hour,
		},
		Store: c.st, EC2: c.ec2, Discord: fd,
		ChannelID: func(context.Context) (string, error) { return "ch", nil },
		Now:       time.Now,
	}

	// EventBridge Scheduler runs this every minute in AWS; the tests cannot wait that long.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Second):
				_ = c.jan.Run(ctx)
			}
		}
	}()
	t.Cleanup(func() { close(stop); wg.Wait() })
	return c
}

func (c *cluster) serveIMDS(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPut:
		_, _ = w.Write([]byte("token"))
	case strings.HasSuffix(r.URL.Path, "/instance-id"):
		_, _ = w.Write([]byte("i-integration"))
	case strings.HasSuffix(r.URL.Path, "/public-ipv4"):
		for i := 0; i < 20; i++ {
			if ci, err := inspect(c.node); err == nil && ci.ip != "" {
				_, _ = w.Write([]byte(ci.ip))
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		http.Error(w, "no ip", http.StatusNotFound)
	default:
		http.NotFound(w, r)
	}
}

type fakeDiscord struct{}

func (fakeDiscord) PostMessage(context.Context, string, discord.Message) (string, error) {
	return "msg", nil
}

func (fakeDiscord) UpdateMessage(context.Context, string, string, discord.Message) error { return nil }

// invoker runs the async half of the Discord Lambda in-process instead of through Lambda.
type invoker struct{ c *cluster }

func (i invoker) InvokeAsync(ctx context.Context, payload []byte) error {
	var ev interactions.AsyncEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return err
	}
	go func() { _ = i.c.inter.HandleAsync(context.Background(), ev) }()
	return nil
}

type client struct {
	key wgtypes.Key
	sid string
}

func (c *cluster) req(method, path, user, body string) events.LambdaFunctionURLResponse {
	r := events.LambdaFunctionURLRequest{Body: body}
	r.RequestContext.Authorizer = sessionapi.SSOCaller(user)
	r.RequestContext.HTTP.Method, r.RequestContext.HTTP.Path = method, path
	return c.api.Handle(context.Background(), r)
}

func (c *cluster) create(user string) *client {
	key, err := wgtypes.GeneratePrivateKey()
	require.NoError(c.t, err)
	r := c.req("POST", "/session", user, `{"public_key":"`+key.PublicKey().String()+`"}`)
	require.Equal(c.t, 202, r.StatusCode, r.Body)
	var m map[string]string
	require.NoError(c.t, json.Unmarshal([]byte(r.Body), &m))
	return &client{key: key, sid: m["session_id"]}
}

func (c *cluster) press(action, sid string) {
	body := fmt.Sprintf(`{"type":3,"data":{"custom_id":"%s:%s"},"member":{"user":{"id":"u1","username":"admin"}},"message":{"id":"msg"}}`, action, sid)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r := c.inter.Handle(context.Background(), events.LambdaFunctionURLRequest{
		Body: body,
		Headers: map[string]string{
			"x-signature-timestamp": ts,
			"x-signature-ed25519":   hex.EncodeToString(ed25519.Sign(c.discordSK, append([]byte(ts), body...))),
		},
	})
	require.Equal(c.t, 200, r.StatusCode, r.Body)
}

type status struct {
	Status string `json:"status"`
	Config *struct {
		Endpoint string `json:"endpoint"`
		Server   string `json:"server_public_key"`
		Address  string `json:"address"`
	} `json:"config"`
}

func (c *cluster) status(user, sid string) status {
	r := c.req("GET", "/session/"+sid, user, "")
	require.Equal(c.t, 200, r.StatusCode, r.Body)
	var s status
	require.NoError(c.t, json.Unmarshal([]byte(r.Body), &s))
	return s
}

func (c *cluster) waitStatus(user, sid, want string, d time.Duration) status {
	var last status
	c.eventually(d, fmt.Sprintf("session %s to be %s", sid, want), func() bool {
		last = c.status(user, sid)
		return last.Status == want
	}, func() string { return fmt.Sprintf("last status %q", last.Status) })
	return last
}

func (c *cluster) eventually(d time.Duration, what string, cond func() bool, detail ...func() string) {
	c.t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(time.Second)
	}
	msg := "timed out waiting for " + what
	for _, f := range detail {
		msg += "; " + f()
	}
	c.t.Fatalf("%s\n%s", msg, c.nodeLog())
}

func (c *cluster) nodeLog() string {
	out, _ := ctr("logs", c.node)
	return "node log:\n" + out
}

func (c *cluster) nodePeers() []string {
	out, err := ctr("exec", c.node, "wg", "show", "wg0", "peers")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

func (c *cluster) running() bool {
	st, _ := c.ec2.State(context.Background())
	return st == ec2ctl.StateRunning
}

// ping brings up a WireGuard client in its own VM and pings the server's tunnel address.
func (c *cluster) ping(cl *client, st status) error {
	require.NotNil(c.t, st.Config)
	script := `set -e
wireguard-go wg0
printf '[Interface]\nPrivateKey = %s\n[Peer]\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = 10.66.0.1/32\nPersistentKeepalive = 1\n' "$KEY" "$SERVER" "$ENDPOINT" >/tmp/c.conf
wg setconf wg0 /tmp/c.conf
ip addr add "$ADDR" dev wg0
ip link set wg0 up
ip route add 10.66.0.1/32 dev wg0
ping -c 3 -W 3 10.66.0.1`
	out, err := ctr("run", "--rm", "--cap-add", "CAP_NET_ADMIN",
		"-e", "KEY="+cl.key.String(), "-e", "SERVER="+st.Config.Server,
		"-e", "ENDPOINT="+st.Config.Endpoint, "-e", "ADDR="+st.Config.Address,
		imageTag, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
