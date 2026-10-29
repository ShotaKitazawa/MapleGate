package discord

import "fmt"

const (
	InteractionPing      = 1
	InteractionCommand   = 2
	InteractionComponent = 3

	ResponsePong                  = 1
	ResponseChannelMessage        = 4
	ResponseDeferredUpdateMessage = 6

	FlagEphemeral = 64

	styleSuccess = 3
	styleDanger  = 4
)

type Interaction struct {
	Type int `json:"type"`
	Data struct {
		CustomID string `json:"custom_id"`
		Name     string `json:"name"`
	} `json:"data"`
	Member struct {
		User User `json:"user"`
	} `json:"member"`
	Message struct {
		ID string `json:"id"`
	} `json:"message"`
}

type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
}

func (u User) DisplayName() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

type Message struct {
	Content    string      `json:"content"`
	Components []Component `json:"components"`
}

type Component struct {
	Type       int         `json:"type"`
	Components []Component `json:"components,omitempty"`
	Style      int         `json:"style,omitempty"`
	Label      string      `json:"label,omitempty"`
	CustomID   string      `json:"custom_id,omitempty"`
}

func ApprovalMessage(sid, user string) Message {
	return Message{
		Content: fmt.Sprintf("VPN access requested by **%s** (session `%s`)", user, sid),
		Components: []Component{{
			Type: 1,
			Components: []Component{
				{Type: 2, Style: styleSuccess, Label: "Approve", CustomID: "approve:" + sid},
				{Type: 2, Style: styleDanger, Label: "Deny", CustomID: "deny:" + sid},
			},
		}},
	}
}

// ResultMessage replaces the buttons with the outcome so it doubles as the approval log.
func ResultMessage(sid, user, outcome, approver, at string) Message {
	by := ""
	if approver != "" {
		by = " by " + approver
	}
	return Message{
		Content:    fmt.Sprintf("VPN access for **%s** (session `%s`): **%s**%s at %s", user, sid, outcome, by, at),
		Components: []Component{},
	}
}
