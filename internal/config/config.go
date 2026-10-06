// Package config loads per-service YAML configuration with env-var overrides.
package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

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

// AuthConfig описывает, как аутентифицироваться у удалённого агента.
// Поддерживается только HTTP Basic: встроенный a2aclient.AuthInterceptor умеет
// лишь Bearer, поэтому Basic ставится своим RoundTripper'ом (см. a2abridge).
type AuthConfig struct {
	Type     string `yaml:"type"` // "" или "basic"
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// AgentConfig — один удалённый A2A-агент, с которым умеет говорить оркестратор.
type AgentConfig struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"` // подпись в селекторе UI; пусто → имя из AgentCard
	URL  string `yaml:"url"`  // вытесняет адрес, объявленный в самой карточке
	// CardPath — путь к AgentCard: не все агенты кладут её в канонический
	// /.well-known/agent-card.json.
	CardPath string `yaml:"card_path"`
	// Skill уезжает в metadata.skill каждого сообщения — так внешний агент
	// понимает, какой набор инструментов включать.
	Skill string `yaml:"skill"`
	// Verbatim: при явном выборе этого агента в UI его ответ уходит в браузер
	// без локальной LLM — ни пересказа, ни лишней латентности.
	Verbatim bool   `yaml:"verbatim"`
	Timeout  string `yaml:"timeout"` // таймаут SendMessage, напр. "180s"
	// Description вытесняет вывод из AgentCard: у агента может быть сотня
	// навыков, и промпт локальной модели такого не переживёт.
	Description string     `yaml:"description"`
	Auth        AuthConfig `yaml:"auth"`
	// TLS — клиентский сертификат (mTLS) и доверие к серверу. Живёт отдельно
	// от Auth: это свойство транспорта и сочетается с Basic, а не заменяет его.
	TLS TLSConfig `yaml:"tls,omitempty"`
}

const (
	defaultCardPath     = "/.well-known/agent-card.json"
	defaultAgentTimeout = 120 * time.Second
)

// TLSConfig — пути к PEM-файлам на машине оркестратора. Хранятся именно пути,
// а не содержимое: приватный ключ не должен ходить через HTTP API настроек и
// оседать в overlay-файле.
type TLSConfig struct {
	CertFile string `yaml:"cert_file,omitempty"` // клиентский сертификат, цепочка допустима
	KeyFile  string `yaml:"key_file,omitempty"`  // его приватный ключ
	// CAFile — корни для проверки сервера; пусто → системные.
	CAFile string `yaml:"ca_file,omitempty"`
	// InsecureSkipVerify отключает проверку сертификата сервера целиком —
	// только для стенда с самоподписанным сертификатом без CA под рукой.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify,omitempty"`
}

// Enabled — задано ли хоть что-то из TLS-настроек.
func (t TLSConfig) Enabled() bool { return t != TLSConfig{} }

// ClientConfig читает файлы и собирает *tls.Config. Зовётся и при валидации:
// битый путь должен ронять загрузку конфига или сохранение формы, а не
// всплывать на первом запросе к агенту.
func (t TLSConfig) ClientConfig() (*tls.Config, error) {
	if (t.CertFile == "") != (t.KeyFile == "") {
		return nil, fmt.Errorf("tls: cert_file and key_file must be set together")
	}
	c := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: t.InsecureSkipVerify} //nolint:gosec // явный выбор пользователя
	if t.CertFile != "" {
		pair, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("tls: client certificate: %w", err)
		}
		c.Certificates = []tls.Certificate{pair}
	}
	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, fmt.Errorf("tls: ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls: ca_file %s: no PEM certificates found", t.CAFile)
		}
		c.RootCAs = pool
	}
	return c, nil
}

// TimeoutDuration возвращает таймаут SendMessage, подставляя значение по
// умолчанию. Разбираемость строки уже проверена при загрузке конфига.
func (a AgentConfig) TimeoutDuration() time.Duration {
	if a.Timeout == "" {
		return defaultAgentTimeout
	}
	d, err := time.ParseDuration(a.Timeout)
	if err != nil || d <= 0 {
		return defaultAgentTimeout
	}
	return d
}

type OrchestratorConfig struct {
	ListenAddr string `yaml:"listen_addr"` // used only in --web mode
	PublicURL  string `yaml:"public_url"`  // used only in --web mode
	// WorkerURL — исторический одиночный агент. Сохранён ради совместимости:
	// если agents: пуст, из него синтезируется единственная запись.
	WorkerURL  string        `yaml:"worker_url"`
	Agents     []AgentConfig `yaml:"agents"`
	A2ALogPath string        `yaml:"a2a_log_path"` // file for A2A protocol trace; empty disables
	// AgentsOverlayPath — файл с правками списка агентов, сделанными из UI.
	// Лежит отдельно от этого конфига: тот рукописный, и машинная перезапись
	// стёрла бы объясняющие комментарии.
	AgentsOverlayPath string    `yaml:"agents_overlay_path"`
	LLM               LLMConfig `yaml:"llm"`
}

var agentIDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// AgentEnvVar возвращает имя переменной окружения для поля агента: пароль
// незачем держать в файле, а адрес приходится подменять при запуске в
// контейнере, а с ним и пути к сертификатам. field — "URL", "PASSWORD",
// "TLS_CERT", "TLS_KEY" или "TLS_CA".
func AgentEnvVar(id, field string) string {
	return "A2A_AGENT_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_" + field
}

// ValidateAgent проверяет одну запись агента — то же, что делает загрузка
// конфига, но без env-перекрытий и без проверки на дубликаты. Отдельно нужна
// затем, что запись из UI проверяется до того, как попадёт в список.
func ValidateAgent(a AgentConfig) error {
	if !agentIDRe.MatchString(a.ID) {
		return fmt.Errorf("agent id %q must match %s", a.ID, agentIDRe)
	}
	if a.URL == "" {
		return fmt.Errorf("agent %q: url is required", a.ID)
	}
	if a.Timeout != "" {
		if d, err := time.ParseDuration(a.Timeout); err != nil || d <= 0 {
			return fmt.Errorf("agent %q: bad timeout %q", a.ID, a.Timeout)
		}
	}
	switch a.Auth.Type {
	case "", "basic":
	default:
		return fmt.Errorf("agent %q: unsupported auth type %q", a.ID, a.Auth.Type)
	}
	if a.TLS.Enabled() {
		// По http:// сертификат молча не участвовал бы в обмене — пусть лучше
		// это будет видно сразу.
		if !strings.HasPrefix(strings.ToLower(a.URL), "https://") {
			return fmt.Errorf("agent %q: tls settings require an https:// url", a.ID)
		}
		if _, err := a.TLS.ClientConfig(); err != nil {
			return fmt.Errorf("agent %q: %w", a.ID, err)
		}
	}
	return nil
}

// NormalizeAgents подставляет умолчания и env-перекрытия, затем валидирует
// список. Применяется и к списку из YAML, и к слитому с overlay — поэтому
// env остаётся последним словом в обоих случаях.
func NormalizeAgents(agents []AgentConfig) error {
	seen := make(map[string]bool, len(agents))
	for i := range agents {
		a := &agents[i]
		// Адрес перекрывается окружением: в контейнере агент живёт по другому
		// имени, чем на машине разработчика, а конфиг один и тот же.
		if u := os.Getenv(AgentEnvVar(a.ID, "URL")); u != "" {
			a.URL = u
		}
		// Пути к сертификатам — по той же причине: в контейнере файлы
		// монтируются в другое место.
		ApplyTLSEnv(a.ID, &a.TLS)
		if err := ValidateAgent(*a); err != nil {
			return fmt.Errorf("orchestrator config: %w", err)
		}
		if seen[a.ID] {
			return fmt.Errorf("orchestrator config: duplicate agent id %q", a.ID)
		}
		seen[a.ID] = true
		if a.CardPath == "" {
			a.CardPath = defaultCardPath
		}
		if p := os.Getenv(AgentEnvVar(a.ID, "PASSWORD")); p != "" {
			a.Auth.Password = p
		}
	}
	return nil
}

// ApplyTLSEnv перекрывает пути к PEM-файлам переменными окружения
// A2A_AGENT_<ID>_TLS_CERT / _TLS_KEY / _TLS_CA и возвращает, какие поля
// перекрыты: "tlsCert", "tlsKey", "tlsCa" — те же имена видит экран настроек.
func ApplyTLSEnv(id string, t *TLSConfig) (locked []string) {
	for _, f := range []struct {
		env, name string
		dst       *string
	}{
		{"TLS_CERT", "tlsCert", &t.CertFile},
		{"TLS_KEY", "tlsKey", &t.KeyFile},
		{"TLS_CA", "tlsCa", &t.CAFile},
	} {
		if v := os.Getenv(AgentEnvVar(id, f.env)); v != "" {
			*f.dst = v
			locked = append(locked, f.name)
		}
	}
	return locked
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
	c.AgentsOverlayPath = env("A2A_AGENTS_OVERLAY_PATH", c.AgentsOverlayPath)
	if c.AgentsOverlayPath == "" {
		c.AgentsOverlayPath = "configs/agents.local.yaml"
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://localhost:8080"
	}
	c.LLM.applyEnv()
	// Совместимость: одиночный worker_url становится единственным агентом.
	if len(c.Agents) == 0 && c.WorkerURL != "" {
		c.Agents = []AgentConfig{{ID: "orders", Name: "Агент заказов", URL: c.WorkerURL}}
	}
	if len(c.Agents) == 0 {
		return c, fmt.Errorf("orchestrator config: at least one agent (agents: or worker_url:) is required")
	}
	if err := NormalizeAgents(c.Agents); err != nil {
		return c, err
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
