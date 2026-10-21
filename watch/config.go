package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config is axon.json: the site, the token, and optionally the
// chat log folder and in-game -> site channel mappings. The site's
// channels page hands out a filled-in copy with each new token.
type Config struct {
	Server   string           `json:"server"`
	Token    string           `json:"token"`
	Logs     string           `json:"logs,omitempty"`
	Channels map[string]int64 `json:"channels,omitempty"`
}

// Template is the config written when there is none yet.
var Template = Config{Server: "https://", Token: "paste your token here", Channels: map[string]int64{}}

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

// Check says what is missing or wrong, in words for a player.
func (c Config) Check() error {
	switch {
	case !strings.HasPrefix(c.Server, "https://") && !strings.HasPrefix(c.Server, "http://"):
		return errors.New(`"server" must be the site's address, like "https://evesynapse.example"`)
	case strings.TrimRight(c.Server, "/") == "https:" || strings.TrimRight(c.Server, "/") == "http:":
		return errors.New(`"server" is empty: put the site's address there`)
	case !strings.HasPrefix(c.Token, "esw_"):
		return errors.New(`"token" is not set: make a token on the site's Intel channels page and paste it here`)
	}
	return nil
}

// WriteTemplate writes an empty config for the player to fill in.
func WriteTemplate(path string) error {
	b, _ := json.MarshalIndent(Template, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
