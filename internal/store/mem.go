package store

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Mem is an in-memory implementation of Store and NodeStore for tests.
type Mem struct {
	mu       sync.Mutex
	sessions map[string]Session
	work     map[string]Work
	active   map[string]activeEntry
	leases   map[string]string
	node     *Node
	health   Health
}

type activeEntry struct {
	sid     string
	expires time.Time
}

func NewMem() *Mem {
	return &Mem{
		sessions: map[string]Session{},
		work:     map[string]Work{},
		active:   map[string]activeEntry{},
		leases:   map[string]string{},
	}
}

func (m *Mem) CreateSession(_ context.Context, s Session, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.active[s.User]; ok && a.expires.After(now) {
		return ErrActive
	}
	m.sessions[s.ID] = s
	m.work[s.ID] = Work{
		SID: s.ID, User: s.User, Phase: s.Status, PublicKey: s.PublicKey, Address: s.Address,
		ExpiresAt: s.ExpiresAt, DiscordMessageID: s.DiscordMessageID,
	}
	m.active[s.User] = activeEntry{sid: s.ID, expires: s.ExpiresAt}
	return nil
}

func (m *Mem) GetSession(_ context.Context, id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	return s, nil
}

func (m *Mem) ActiveSessionID(_ context.Context, user string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.active[user]
	if !ok {
		return "", ErrNotFound
	}
	return a.sid, nil
}

func (m *Mem) SetMessageID(_ context.Context, id, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrNotFound
	}
	s.DiscordMessageID = messageID
	m.sessions[id] = s
	w := m.work[id]
	w.DiscordMessageID = messageID
	m.work[id] = w
	return nil
}

func (m *Mem) SetMsgSynced(_ context.Context, id string, synced bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrNotFound
	}
	s.MsgSynced = synced
	m.sessions[id] = s
	if w, ok := m.work[id]; ok {
		w.MsgSynced = synced
		m.work[id] = w
	}
	return nil
}

func (m *Mem) Transition(_ context.Context, id string, from []Status, to Status, u Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || !contains(from, s.Status) {
		return ErrConflict
	}
	w, ok := m.work[id]
	if !ok {
		return ErrConflict
	}
	s.Status, w.Phase = to, to
	if u.Address != "" {
		s.Address, w.Address = u.Address, u.Address
	}
	if u.Approver != "" {
		s.Approver = u.Approver
	}
	if u.Error != "" {
		s.Error = u.Error
	}
	if u.CloseReason != "" {
		s.CloseReason, w.CloseReason = u.CloseReason, u.CloseReason
	}
	if !u.ApprovedAt.IsZero() {
		s.ApprovedAt, w.ApprovedAt = u.ApprovedAt, u.ApprovedAt
	}
	if !u.ExpiresAt.IsZero() {
		s.ExpiresAt, w.ExpiresAt = u.ExpiresAt, u.ExpiresAt
		if a, ok := m.active[s.User]; ok && a.sid == id {
			a.expires = u.ExpiresAt
			m.active[s.User] = a
		}
	}
	m.sessions[id], m.work[id] = s, w
	if to.Terminal() {
		m.clearActive(s.User, id)
	}
	return nil
}

func (m *Mem) clearActive(user, sid string) {
	if a, ok := m.active[user]; ok && a.sid == sid {
		delete(m.active, user)
	}
}

func (m *Mem) ClearActive(_ context.Context, user, sid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearActive(user, sid)
	return nil
}

func (m *Mem) LeaseIP(_ context.Context, addr, sid string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.leases[addr]; ok {
		return ErrConflict
	}
	m.leases[addr] = sid
	return nil
}

func (m *Mem) ReleaseIP(_ context.Context, addr, sid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leases[addr] == sid {
		delete(m.leases, addr)
	}
	return nil
}

func (m *Mem) ListWork(_ context.Context) ([]Work, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Work, 0, len(m.work))
	for _, w := range m.work {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SID < out[j].SID })
	return out, nil
}

func (m *Mem) GetWork(_ context.Context, sid string) (Work, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.work[sid]
	if !ok {
		return Work{}, ErrNotFound
	}
	return w, nil
}

func (m *Mem) MarkRemoved(ctx context.Context, sid string, at time.Time) error {
	return m.ReportRemoved(ctx, sid, at)
}

func (m *Mem) DeleteWork(_ context.Context, sid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.work[sid]
	if !ok {
		return nil
	}
	if w.RemovedAt.IsZero() {
		return ErrConflict
	}
	delete(m.work, sid)
	return nil
}

func (m *Mem) GetNode(_ context.Context) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.node == nil {
		return Node{}, ErrNotFound
	}
	return *m.node, nil
}

func (m *Mem) PutNode(_ context.Context, n Node, now time.Time, takeoverAfter time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.node != nil && m.node.InstanceID != n.InstanceID && now.Sub(m.node.LastSeen) < takeoverAfter {
		return ErrConflict
	}
	cp := n
	m.node = &cp
	return nil
}

func (m *Mem) GetHealth(_ context.Context) (Health, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.health, nil
}

func (m *Mem) PutHealth(_ context.Context, h Health) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.health = h
	return nil
}

func (m *Mem) updateWork(sid string, f func(*Work)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.work[sid]
	if !ok {
		return ErrNotFound
	}
	f(&w)
	m.work[sid] = w
	return nil
}

func (m *Mem) ReportApplied(_ context.Context, sid, bootID string, at time.Time) error {
	return m.updateWork(sid, func(w *Work) { w.AppliedAt, w.AppliedBootID, w.RemovedAt = at, bootID, time.Time{} })
}

func (m *Mem) ReportRemoved(_ context.Context, sid string, at time.Time) error {
	return m.updateWork(sid, func(w *Work) { w.RemovedAt = at })
}

func (m *Mem) ReportHandshake(_ context.Context, sid string, at time.Time) error {
	return m.updateWork(sid, func(w *Work) { w.LastHandshakeAt = at })
}

func (m *Mem) ReportError(_ context.Context, sid, msg string) error {
	return m.updateWork(sid, func(w *Work) { w.NodeError = msg })
}

// DropSession deletes only the META half, as TTL expiry would.
func (m *Mem) DropSession(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// Leases and ActiveUsers expose bookkeeping that tests assert on once everything should have been reclaimed.
func (m *Mem) Leases() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.leases))
	for k, v := range m.leases {
		out[k] = v
	}
	return out
}

func (m *Mem) ActiveUsers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for u := range m.active {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

func (m *Mem) Sessions() []Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
