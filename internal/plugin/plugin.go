// Package plugin defines how integrations (Gmail, Calendar, Slack, …) plug
// into the assistant. The core engine only knows these interfaces; it never
// imports a specific integration.
//
// Every plugin provides tools. Beyond that, a plugin implements only the
// optional capabilities that make sense for it (interface segregation): a
// read-only plugin implements nothing else, while one with outward-facing
// writes also implements Executor (and optionally Editor and Deduper).
package plugin

import (
	"context"
	"encoding/json"
)

// Tool is one function the model can call.
//
// A read tool sets Run and executes immediately. A write tool (Write: true)
// sets Propose and returns a Proposal instead of acting; the engine decides
// whether it waits for the user's approval, checks for duplicates, and later
// hands it back to the plugin's Executor.
type Tool struct {
	Name        string
	Description string
	Properties  map[string]any // JSON Schema properties of the input object
	Required    []string

	Write   bool
	Run     func(ctx context.Context, input json.RawMessage) (any, error)
	Propose func(ctx context.Context, input json.RawMessage) (Proposal, error)
}

// Proposal describes an outward-facing change, e.g. "send this email".
type Proposal struct {
	Kind    string // routed to the Executor that lists this kind
	Summary string // one line shown to the user
	Payload any    // everything needed to execute it later (stored as JSON)
}

// Plugin is an integration. Name is a short id such as "gmail" or "slack".
type Plugin interface {
	Name() string
	Tools() []Tool
}

// Executor performs approved proposals of the kinds it lists.
type Executor interface {
	Kinds() []string
	Execute(ctx context.Context, kind string, payload json.RawMessage) (string, error)
}

// Editor lets the user (via the model) change a proposal while it is still
// waiting for approval, instead of queuing a second copy.
type Editor interface {
	// EditFields returns the JSON Schema properties accepted when editing a
	// proposal of this kind, or nil if it can't be edited.
	EditFields(kind string) map[string]any
	// Edit applies changes to a pending payload and returns the new proposal.
	Edit(ctx context.Context, kind string, payload, changes json.RawMessage) (Proposal, error)
}

// Deduper reports whether a new proposal duplicates one already pending.
type Deduper interface {
	Duplicate(kind string, pending, proposed json.RawMessage) bool
}

// Guide adds plugin-specific instructions to the model's system prompt.
type Guide interface {
	Guidance() string
}
