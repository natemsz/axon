package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Config is axon.json: the site, the paired credential, and
// optionally the chat log folder and in-game -> site channel
// mappings. Pairing (keys.go) fills the credential in; the device
// private key lives beside it in axon.key, never inside it.
type Config struct {
	Server   string           `json:"server"`
	Token    string           `json:"token"`
	Logs     string           `json:"logs,omitempty"`
	Channels map[string]int64 `json:"channels,omitempty"`
	// Grab, off unless set, turns on the overlay's clipboard grab
	// (Windows): one press of Ctrl+Alt+V reads the clipboard once
	// and posts what it holds to the overlay's channel. Nothing is
	// registered, hooked or watched while it is off (overlay.go in
	// package gui). It is never set by pairing or the template.
	Grab bool `json:"grab,omitempty"`
}

// Template is the config written when there is none yet.
var Template = Config{Server: "https://", Token: "pair Axon to fill this in", Channels: map[string]int64{}}

// LoadConfig reads and checks a config file.
func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return c, c.Check()
}

// CheckServer says whether a site address may be talked to at
// all: HTTPS, except for a site on this same computer (a local
// development server), where plain HTTP never leaves the machine.
func CheckServer(server string) error {
	u, err := url.Parse(strings.TrimSpace(server))
	if err != nil || u.Host == "" {
		return errors.New(`"server" must be the site's address, like "https://evesynapse.example"`)
	}
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && isLoopbackHost(u.Hostname()):
		return nil
	case u.Scheme == "http":
		return errors.New(`"server" must start with https:// so your credential and intel stay encrypted (http:// is only for a site on this computer)`)
	default:
		return errors.New(`"server" must be the site's address, like "https://evesynapse.example"`)
	}
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Check says what is missing or wrong, in words for a player.
func (c Config) Check() error {
	if err := CheckServer(c.Server); err != nil {
		return err
	}
	if !strings.HasPrefix(c.Token, "esw_") {
		return errors.New(`"token" is not set: pair Axon from its settings page with a code from the site's Intel channels page`)
	}
	return nil
}

// WriteTemplate writes an empty config for the player to fill in.
func WriteTemplate(path string) error {
	b, _ := json.MarshalIndent(Template, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
