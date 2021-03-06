package tail

import (
	"bqtail/base"
	"bqtail/tail/config"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/viant/afs"
	"github.com/viant/afs/cache"
	"os"
	"strings"
)

//Config represents a tail config
type Config struct {
	base.Config
	config.Ruleset
}

//Init initializes config
func (c *Config) Init(ctx context.Context, fs afs.Service) error {
	err := c.Config.Init(ctx)
	if err != nil {
		return err
	}
	if err = c.Ruleset.Init(ctx, fs, c.ProjectID); err != nil {
		return err
	}
	c.initLoadedRules()
	return nil
}

func (c *Config) initLoadedRules() {
	if len(c.Rules) == 0 {
		return
	}
	for _, route := range c.Rules {
		if route.Batch != nil {
			baseURL := c.SyncTaskURL
			if route.Async {
				baseURL = c.AsyncTaskURL
			}
			route.Batch.Init(baseURL)
		}
	}
}

//ReloadIfNeeded reloads rules if needed
func (c *Config) ReloadIfNeeded(ctx context.Context, fs afs.Service) error {
	changed, err := c.Ruleset.ReloadIfNeeded(ctx, fs)
	if err != nil {
		return err
	}
	if changed {
		c.initLoadedRules()
	}
	return err
}

//Validate checks if config is valid
func (c *Config) Validate() error {
	err := c.Config.Validate()
	if err != nil {
		return err
	}
	if c.CorruptedFileURL == "" {
		return fmt.Errorf("corruptedFileURL were empty")
	}
	if c.Ruleset.UsesBatchInSyncMode() && c.SyncTaskURL == "" {
		return fmt.Errorf("syncTaskURL were empty")
	}
	return c.Ruleset.Validate()
}

//NewConfigFromEnv creates config from env
func NewConfigFromEnv(ctx context.Context, key string) (*Config, error) {
	if key == "" {
		return nil, errors.New("os env cfg key was empty")
	}
	data := os.Getenv(key)
	if data == "" {
		return nil, fmt.Errorf("env.%v was empty", key)
	}
	cfg := &Config{}
	err := json.NewDecoder(strings.NewReader(data)).Decode(cfg)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode config :%s", data)
	}

	if err = cfg.Init(ctx, afs.New()); err != nil {
		return nil, err
	}
	err = cfg.Validate()
	return cfg, err
}

//NewConfigFromURL creates new config from URL
func NewConfigFromURL(ctx context.Context, URL string) (*Config, error) {
	storageService := cache.Singleton(URL)
	reader, err := storageService.DownloadWithURL(ctx, URL)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	err = json.NewDecoder(reader).Decode(cfg)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode config :%s", URL)
	}
	cfg.URL = URL
	if err = cfg.Init(ctx, storageService); err != nil {
		return cfg, err
	}
	err = cfg.Validate()
	return cfg, err
}

//NewConfig creates a new config from env (json or URL)
func NewConfig(ctx context.Context, key string) (*Config, error) {
	if key == "" {
		return nil, fmt.Errorf("config key was empty")
	}
	value := os.Getenv(key)
	if json.Valid([]byte(value)) {
		return NewConfigFromEnv(ctx, key)
	}
	return NewConfigFromURL(ctx, value)
}
