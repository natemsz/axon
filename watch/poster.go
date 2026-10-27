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
// pasted here it goes whole, line by line. A paste is posted as the
// character whose chat log changed most recently, the one at the
// keyboard, and, when no log says, as the account's own character.
type Poster struct {
	Client *Client
	// LogDir is the chat log folder the watcher reads. Empty, or a
	// folder whose freshest log names no listener, posts as the
	// account's own character.
	LogDir string
}

// PrepareLines makes what a paste is posted as. Its lines are
// trimmed, the blank ones dropped, order kept. A paste of more
// than one line goes as its own lines, so the site reads a local
// list line by line; a paste whose first line is already a chat
// line goes as its lines too. A single typed line goes as one
// chat-log line spoken by speaker, stamped in EVE time (UTC),
// like the client's logs.
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
	if len(parts) > 1 || IsMessage(parts[0]) {
		return parts, nil
	}
	line := "[ " + now.UTC().Format("2006.01.02 15:04:05") + " ] " + speaker + " > " + parts[0]
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

// Post sends a paste to a channel, and returns the channel as
// resolved and who the paste is posted as, with where that name
// came from: "Iwaira Itovuod (freshest log)", "Burzrujat
// (account character)". The channel must be one the account may
// post to: the site would refuse another, and saying so here
// names the channel instead.
func (p *Poster) Post(ctx context.Context, channel int64, text string, now time.Time) (PostResult, Channel, string, error) {
	return p.PostWith(ctx, channel, text, now, Overrides{})
}

// PostWith is Post carrying the overlay's overrides (kind, system).
func (p *Poster) PostWith(ctx context.Context, channel int64, text string, now time.Time, ov Overrides) (PostResult, Channel, string, error) {
	account, postable, err := p.Postable(ctx)
	if err != nil {
		return PostResult{}, Channel{}, "", err
	}
	var target Channel
	for _, c := range postable {
		if c.ID == channel {
			target = c
			break
		}
	}
	if target.ID == 0 {
		return PostResult{}, Channel{}, "", fmt.Errorf("channel %d is not one this account can post to", channel)
	}
	speaker, as := account, account+" (account character)"
	if name, ok := FreshestSpeaker(p.LogDir); ok {
		speaker, as = name, name+" (freshest log)"
	}
	lines, err := PrepareLines(speaker, text, now)
	if err != nil {
		return PostResult{}, Channel{}, "", err
	}
	res, err := p.Client.PostLinesWith(ctx, channel, lines, speaker, ov)
	return res, target, as, err
}

// Preview asks the site what it would make of text, read as the
// character at the keyboard would post it.
func (p *Poster) Preview(ctx context.Context, text string) (Preview, error) {
	speaker := ""
	if name, ok := FreshestSpeaker(p.LogDir); ok {
		speaker = name
	}
	return p.Client.Preview(ctx, text, speaker)
}
