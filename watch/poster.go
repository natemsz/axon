package watch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// A Poster sends lines a player pastes into Axon itself (the
// overlay), rather than lines the watcher read out of a chat log.
// The EVE client truncates a local roster pasted into a channel at
// twenty names, so a busy system's roster can never travel by chat;
// pasted here it goes whole. What is posted is a line in the chat
// log's own shape, from the account's own character, so the site
// reads it exactly as it reads a watched line.
type Poster struct {
	Client *Client
}

// PrepareLines makes the chat-log line a paste is posted as. The
// paste is one message, as the client's own chat box makes one
// message of a paste: its lines are trimmed, the blank ones dropped,
// and the rest joined with two spaces — the gap names dragged out of
// the local list carry in a chat line, which the site reads back as
// the roster. A single typed line goes as itself. The line is
// stamped in EVE time (UTC), like the client's logs.
func PrepareLines(speaker, text string, now time.Time) ([]string, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var parts []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	if len(parts) == 0 {
		return nil, errors.New("nothing to send: paste an intel line or a local list first")
	}
	line := "[ " + now.UTC().Format("2006.01.02 15:04:05") + " ] " + speaker + " > " + strings.Join(parts, "  ")
	return []string{line}, nil
}

// Postable lists the channels this account may post to, by name,
// with the account's name: the overlay's targets.
func (p *Poster) Postable(ctx context.Context) (string, []Channel, error) {
	account, list, err := p.Client.Channels(ctx)
	if err != nil {
		return "", nil, err
	}
	out := make([]Channel, 0, len(list))
	for _, c := range list {
		if c.Post {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	if len(out) == 0 {
		return account, nil, errors.New("this account cannot post to any intel channel: make one on the site, or ask a channel's owner for posting access")
	}
	return account, out, nil
}

// Post sends a paste to a channel as the account's character, and
// returns the channel as resolved. The channel must be one the
// account may post to: the site would refuse another, and saying so
// here names the channel instead.
func (p *Poster) Post(ctx context.Context, channel int64, text string, now time.Time) (PostResult, Channel, error) {
	account, postable, err := p.Postable(ctx)
	if err != nil {
		return PostResult{}, Channel{}, err
	}
	var target Channel
	for _, c := range postable {
		if c.ID == channel {
			target = c
			break
		}
	}
	if target.ID == 0 {
		return PostResult{}, Channel{}, fmt.Errorf("channel %d is not one this account can post to", channel)
	}
	lines, err := PrepareLines(account, text, now)
	if err != nil {
		return PostResult{}, Channel{}, err
	}
	res, err := p.Client.PostLines(ctx, channel, lines)
	return res, target, err
}
