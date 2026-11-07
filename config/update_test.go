package config

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestUpdateKeepsConcurrentMutations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := validConfig()
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Update(context.Background(), path, func(c *Config) error {
				if c.Agents[0].Env == nil {
					c.Agents[0].Env = map[string]string{}
				}
				c.Agents[0].Env[fmt.Sprintf("SETTING_%d", i)] = "value"
				return nil
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	actual, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Agents[0].Env) != 12 {
		t.Fatal("concurrent config update lost a mutation")
	}
}
