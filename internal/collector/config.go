package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

// One process may serve several explicitly scoped project connections.
// A connection's key is private to its uploader; tools use its separate local token.
type Connection struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	Endpoint   string `json:"endpoint"`
	Key        string `json:"api_key"`
	LocalToken string `json:"local_token"`
	Outbox     string `json:"outbox"`
	UserID     string `json:"user_id"`
	IDE        string `json:"ide"`
	Privacy    string `json:"privacy"`
}
type Config struct {
	Connections []Connection `json:"connections"`
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	f, err := os.Open(path)
	if err != nil {
		return cfg, errors.New("cannot open collector configuration")
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cfg) != nil {
		return cfg, errors.New("invalid collector configuration")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return cfg, errors.New("expected one configuration object")
	}
	return cfg, nil
}
func Connections(cfg Config) (http.Handler, []*Collector, error) {
	if len(cfg.Connections) < 1 || len(cfg.Connections) > 16 {
		return nil, nil, errors.New("configure 1–16 connections")
	}
	mux := http.NewServeMux()
	collectors := []*Collector{}
	ids := map[string]bool{}
	dirs := map[string]bool{}
	tokens := map[string]bool{}
	valid := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	for _, x := range cfg.Connections {
		dir, err := filepath.Abs(x.Outbox)
		if err != nil || !valid.MatchString(x.ID) || ids[x.ID] || dirs[dir] || tokens[x.LocalToken] || x.ProjectID == "" {
			return nil, nil, errors.New("connections need distinct IDs, outboxes and local tokens, plus project IDs")
		}
		if x.Privacy == "" {
			x.Privacy = "METADATA_ONLY"
		}
		if x.Privacy != "METADATA_ONLY" && x.Privacy != "REDACTED" && x.Privacy != "FULL" {
			return nil, nil, errors.New("invalid privacy mode")
		}
		c, err := New(x.Outbox, x.Endpoint, x.Key, x.LocalToken)
		if err != nil {
			return nil, nil, err
		}
		c.ProjectID = x.ProjectID
		c.UserID = x.UserID
		c.IDE = x.IDE
		c.Privacy = x.Privacy
		mux.Handle("/"+x.ID+"/", http.StripPrefix("/"+x.ID, c.Handler()))
		collectors = append(collectors, c)
		ids[x.ID] = true
		dirs[dir] = true
		tokens[x.LocalToken] = true
	}
	return mux, collectors, nil
}
func RunAll(ctx context.Context, collectors []*Collector) {
	for _, c := range collectors {
		go c.Run(ctx)
	}
}
