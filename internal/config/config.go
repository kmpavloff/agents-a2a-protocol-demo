// Package config loads per-service YAML configuration with env-var overrides.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type LLMConfig struct {
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
	APIKey  string `yaml:"api_key"`
}

type WorkerConfig struct {
	ListenAddr string `yaml:"listen_addr"`
	PublicURL  string `yaml:"public_url"`
	DataPath   string `yaml:"data_path"`
	// OrderLinkBase is the base URL of the customer-facing order page; widgets
	// carry base/<id> links so a client can open the order card. Empty disables
	// links entirely.
	OrderLinkBase string    `yaml:"order_link_base"`
	LLM           LLMConfig `yaml:"llm"`
}

type OrchestratorConfig struct {
	ListenAddr string    `yaml:"listen_addr"` // used only in --web mode
	PublicURL  string    `yaml:"public_url"`  // used only in --web mode
	WorkerURL  string    `yaml:"worker_url"`
	A2ALogPath string    `yaml:"a2a_log_path"` // file for A2A protocol trace; empty disables
	LLM        LLMConfig `yaml:"llm"`
}

func env(key, cur string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return cur
}

func (c *LLMConfig) applyEnv() {
	c.BaseURL = env("LLM_BASE_URL", c.BaseURL)
	c.Model = env("LLM_MODEL", c.Model)
	c.APIKey = env("LLM_API_KEY", c.APIKey)
}

func LoadWorker(path string) (WorkerConfig, error) {
	var c WorkerConfig
	if err := readYAML(path, &c); err != nil {
		return c, err
	}
	c.ListenAddr = env("WORKER_LISTEN_ADDR", c.ListenAddr)
	c.PublicURL = env("WORKER_PUBLIC_URL", c.PublicURL)
	c.DataPath = env("WORKER_DATA_PATH", c.DataPath)
	c.OrderLinkBase = env("ORDER_LINK_BASE", c.OrderLinkBase)
	if c.OrderLinkBase == "" {
		c.OrderLinkBase = "https://shop.example.com/orders"
	}
	c.LLM.applyEnv()
	if c.ListenAddr == "" {
		return c, fmt.Errorf("worker config: listen_addr is required")
	}
	if c.PublicURL == "" {
		return c, fmt.Errorf("worker config: public_url is required")
	}
	if c.DataPath == "" {
		return c, fmt.Errorf("worker config: data_path is required")
	}
	if c.LLM.BaseURL == "" {
		return c, fmt.Errorf("worker config: llm.base_url is required")
	}
	return c, nil
}

func LoadOrchestrator(path string) (OrchestratorConfig, error) {
	var c OrchestratorConfig
	if err := readYAML(path, &c); err != nil {
		return c, err
	}
	c.ListenAddr = env("ORCHESTRATOR_LISTEN_ADDR", c.ListenAddr)
	c.PublicURL = env("ORCHESTRATOR_PUBLIC_URL", c.PublicURL)
	c.WorkerURL = env("WORKER_URL", c.WorkerURL)
	c.A2ALogPath = env("A2A_LOG_PATH", c.A2ALogPath)
	if c.A2ALogPath == "" {
		c.A2ALogPath = "a2a-orchestrator.log"
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://localhost:8080"
	}
	c.LLM.applyEnv()
	if c.WorkerURL == "" {
		return c, fmt.Errorf("orchestrator config: worker_url is required")
	}
	if c.LLM.BaseURL == "" {
		return c, fmt.Errorf("orchestrator config: llm.base_url is required")
	}
	return c, nil
}

func readYAML(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}
