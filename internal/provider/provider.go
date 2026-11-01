// Package provider defines the integration contracts (mail, calendar)
// that the assistant depends on. Concrete implementations live in
// sub-packages (google, mock), so the assistant never imports an SDK.
package provider

import (
	"context"
	"errors"
	"time"
)

// ErrNotConnected is returned when an integration has no credentials yet.
var ErrNotConnected = errors.New("integration not connected")

// ErrReauth means the stored sign-in was rejected (expired, revoked, or
// issued to a different OAuth client); the user must sign in again.
var ErrReauth = errors.New("your Google access expired or was revoked — sign out and sign in again")

// Email is a normalized mail message.
type Email struct {
	ID       string    `json:"id"`
	ThreadID string    `json:"thread_id"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Subject  string    `json:"subject"`
	Snippet  string    `json:"snippet"`
	Body     string    `json:"body,omitempty"`
	Date     time.Time `json:"date"`
	Unread   bool      `json:"unread"`
	Sent     bool      `json:"sent,omitempty"` // written by the user
}

// OutgoingEmail is a message to send or draft.
type OutgoingEmail struct {
	To        []string `json:"to"`
	Cc        []string `json:"cc,omitempty"`
	Bcc       []string `json:"bcc,omitempty"`
	Subject   string   `json:"subject"`
	Body      string   `json:"body"`
	ReplyToID string   `json:"reply_to_id,omitempty"` // message ID being replied to
	// ThreadID and InReplyTo keep an edited draft in its original thread.
	ThreadID  string `json:"thread_id,omitempty"`
	InReplyTo string `json:"in_reply_to,omitempty"`
}

// Draft is a saved, unsent email.
type Draft struct {
	ID      string        `json:"id"`
	Message OutgoingEmail `json:"message"`
	Snippet string        `json:"snippet,omitempty"`
}

// Event is a normalized calendar event.
type Event struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	AllDay      bool      `json:"all_day"`
	Attendees   []string  `json:"attendees,omitempty"`
	Link        string    `json:"link,omitempty"`      // calendar page
	MeetLink    string    `json:"meet_link,omitempty"` // Google Meet join URL
}

// Busy is one busy block from a free/busy lookup.
type Busy struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Availability is one person's free/busy result. Error is set when their
// calendar can't be read (outside the org, not found, not shared).
type Availability struct {
	Busy  []Busy `json:"busy"`
	Error string `json:"error,omitempty"`
}

// TranscriptLine is one spoken line in a meeting transcript.
type TranscriptLine struct {
	Speaker string    `json:"speaker"`
	Time    time.Time `json:"time"`
	Text    string    `json:"text"`
}

// Transcript is a meeting's transcript, possibly from several sessions.
type Transcript struct {
	MeetingCode string           `json:"meeting_code"`
	Lines       []TranscriptLine `json:"lines"`
	Truncated   bool             `json:"truncated,omitempty"`
}

// NewEvent is an event to create.
type NewEvent struct {
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Attendees   []string  `json:"attendees,omitempty"`
}

// EventUpdate changes an existing event. Nil fields are left as they are;
// Attendees, when set, is the complete new attendee list.
type EventUpdate struct {
	Title       *string    `json:"title,omitempty"`
	Description *string    `json:"description,omitempty"`
	Location    *string    `json:"location,omitempty"`
	Start       *time.Time `json:"start,omitempty"`
	End         *time.Time `json:"end,omitempty"`
	Attendees   *[]string  `json:"attendees,omitempty"`
}

// Mail is the Gmail-like contract.
type Mail interface {
	Connected() bool
	ListMessages(ctx context.Context, query string, max int) ([]Email, error)
	GetMessage(ctx context.Context, id string) (Email, error)
	// Thread returns a conversation's messages, oldest first, without bodies.
	Thread(ctx context.Context, threadID string) ([]Email, error)
	CreateDraft(ctx context.Context, msg OutgoingEmail) (string, error)
	Send(ctx context.Context, msg OutgoingEmail) (string, error)
	ListDrafts(ctx context.Context, max int) ([]Draft, error)
	GetDraft(ctx context.Context, id string) (Draft, error)
	UpdateDraft(ctx context.Context, id string, msg OutgoingEmail) error
	SendDraft(ctx context.Context, id string) (string, error)
	DeleteDraft(ctx context.Context, id string) error
}

// Calendar is the Google Calendar-like contract.
type Calendar interface {
	Connected() bool
	// ListEvents and GetEvent read calendarID ("" = the user's own), which
	// for a colleague is their email and works only if they share with you.
	ListEvents(ctx context.Context, calendarID string, from, to time.Time) ([]Event, error)
	GetEvent(ctx context.Context, calendarID, id string) (Event, error)
	// FreeBusy returns busy blocks per calendar ID; inside a Workspace org
	// this works for colleagues without any sharing.
	FreeBusy(ctx context.Context, calendarIDs []string, from, to time.Time) (map[string]Availability, error)
	CreateEvent(ctx context.Context, ev NewEvent) (Event, error)
	UpdateEvent(ctx context.Context, id string, upd EventUpdate) (Event, error)
	DeleteEvent(ctx context.Context, id string) error
}

// Meetings reads Google Meet records (transcripts of past meetings).
type Meetings interface {
	Connected() bool
	// Transcript returns the transcript for a meeting code like "abc-defg-hij".
	Transcript(ctx context.Context, meetingCode string) (Transcript, error)
}

// ErrNoTranscript means the meeting has no transcript (not recorded, or the
// user isn't the organizer / wasn't given access).
var ErrNoTranscript = errors.New("no transcript found for this meeting")

// Channel is a chat channel or DM.
type Channel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsPrivate bool   `json:"is_private"`
	IsIM      bool   `json:"is_im"`
}

// ChatMessage is a normalized chat message.
type ChatMessage struct {
	Channel     string    `json:"channel"`
	ChannelName string    `json:"channel_name,omitempty"`
	User        string    `json:"user"`
	Text        string    `json:"text"`
	TS          string    `json:"ts"`
	ThreadTS    string    `json:"thread_ts,omitempty"`
	Time        time.Time `json:"time"`
	Permalink   string    `json:"permalink,omitempty"`
}

// Chat is the Slack-like contract. Calls run as the signed-in user.
type Chat interface {
	ListChannels(ctx context.Context) ([]Channel, error)
	History(ctx context.Context, channel string, limit int) ([]ChatMessage, error)
	Search(ctx context.Context, query string, limit int) ([]ChatMessage, error)
	Mentions(ctx context.Context, limit int) ([]ChatMessage, error)
	// Post sends text to a channel (name or ID), optionally in a thread, and
	// returns the message timestamp.
	Post(ctx context.Context, channel, text, threadTS string) (string, error)
}
