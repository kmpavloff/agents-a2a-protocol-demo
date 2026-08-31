// Package agentstore хранит правки списка агентов, сделанные из веб-интерфейса.
//
// Базовый список приезжает из configs/orchestrator.yaml и остаётся рукописным:
// в нём живут комментарии, объясняющие каждого агента, и переписывать его
// машинно значило бы их потерять. Правки UI ложатся отдельным overlay-файлом
// поверх базового списка.
package agentstore

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"github.com/kmpavloff/agents-a2a-protocol-demo/internal/config"
)

// Override — одна overlay-запись: агент целиком плюс признак «скрыт».
//
// Hidden живёт здесь, а не в config.AgentConfig: он описывает не агента, а
// решение UI убрать его из списка. Агент, заведённый в YAML, иначе не
// удаляется — базовый файл мы не трогаем.
type Override struct {
	config.AgentConfig `yaml:",inline"`
	Hidden             bool `yaml:"hidden,omitempty"`
}

// merge накладывает overlay на базовый список.
//
// Запись с совпавшим id заменяет базовую целиком и остаётся на её месте:
// порядок задаёт пункты селектора в браузере и выбор агента для терминального
// REPL, и менять его из-за правки адреса нельзя. Записи с незнакомыми id — это
// заведённые через UI агенты, они уходят в конец в порядке overlay.
func merge(base []config.AgentConfig, over []Override) []config.AgentConfig {
	byID := make(map[string]Override, len(over))
	for _, o := range over {
		byID[o.ID] = o
	}
	used := make(map[string]bool, len(over))
	out := make([]config.AgentConfig, 0, len(base)+len(over))
	for _, b := range base {
		o, ok := byID[b.ID]
		if !ok {
			out = append(out, b)
			continue
		}
		used[o.ID] = true
		if o.Hidden {
			continue
		}
		out = append(out, o.AgentConfig)
	}
	for _, o := range over {
		if used[o.ID] || o.Hidden {
			continue
		}
		out = append(out, o.AgentConfig)
	}
	return out
}

// ErrNotFound — агента с таким id нет; ErrExists — уже есть. Отдельные ошибки
// нужны обработчику: 404 и 409 против 400 у невалидной записи.
var (
	ErrNotFound = errors.New("agent not found")
	ErrExists   = errors.New("agent already exists")
)

// Происхождение записи. Значения уезжают в JSON как есть, поэтому живут
// константами: их читает и фронтенд, и HTTP-слой, выводящий из них право на
// сброс.
const (
	SourceFile = "file" // только из orchestrator.yaml, overlay-записи нет
	SourceUI   = "ui"   // есть overlay-запись, сделанная из интерфейса
)

// Record — запись для экрана настроек: действующий конфиг агента плюс
// происхождение. Пароль сюда не попадает никогда — только признак, что он
// задан.
type Record struct {
	config.AgentConfig
	Hidden      bool
	Source      string // SourceFile или SourceUI
	HasPassword bool
	EnvLocked   []string // поля, перекрытые окружением: "url", "password"
	// InFile — есть ли у агента версия в базовом (рукописном) списке. Source
	// говорит лишь о том, есть ли overlay-запись, а её наличие означает разное
	// для двух разных сущностей: файловый агент с правкой из UI (сброс вернёт
	// версию из YAML) и агент, целиком заведённый через UI (базовой версии нет
	// вовсе, и «сброс» для него — это удаление). InFile разводит их для кнопки
	// «Сбросить к конфигу».
	InFile bool
}

// Store владеет списком агентов: рукописной базой из orchestrator.yaml и
// overlay-файлом с правками из UI.
type Store struct {
	overlayPath string

	mu       sync.Mutex
	base     []config.AgentConfig
	over     []Override
	merged   []config.AgentConfig // слитый и нормализованный, без скрытых
	onChange func([]config.AgentConfig)
}

// New читает overlay и складывает его с базовым списком. Битый overlay — это
// отказ на старте, а не молчаливое игнорирование: агенты, заведённые через UI,
// не должны исчезать незаметно.
func New(base []config.AgentConfig, overlayPath string) (*Store, error) {
	over, err := loadOverlay(overlayPath)
	if err != nil {
		return nil, err
	}
	s := &Store{overlayPath: overlayPath, base: base, over: over}
	merged, err := s.mergeNormalized(over)
	if err != nil {
		return nil, err
	}
	s.merged = merged
	return s, nil
}

// OnChange задаёт подписчика, которому уезжает новый список после каждой
// удачной правки. Через него живой Registry узнаёт об изменениях.
func (s *Store) OnChange(fn func([]config.AgentConfig)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

// mergeNormalized складывает базу с overlay и прогоняет через ту же
// нормализацию, что и загрузка конфига, — так env-перекрытия остаются
// последним словом и после правок из UI.
func (s *Store) mergeNormalized(over []Override) ([]config.AgentConfig, error) {
	merged := merge(s.base, over)
	if err := config.NormalizeAgents(merged); err != nil {
		return nil, err
	}
	return merged, nil
}

// Agents возвращает действующий список — тот, по которому живёт Registry.
func (s *Store) Agents() []config.AgentConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.merged)
}

// Records описывает агентов для экрана настроек, включая скрытых: вернуть
// удалённого агента иначе можно было бы только правкой файла.
func (s *Store) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	byID := make(map[string]Override, len(s.over))
	for _, o := range s.over {
		byID[o.ID] = o
	}
	out := make([]Record, 0, len(s.base)+len(s.over))
	seen := make(map[string]bool, len(s.over))
	add := func(a config.AgentConfig, hidden, fromOverlay bool) {
		source := SourceFile
		if fromOverlay {
			source = SourceUI
		}
		r := Record{AgentConfig: a, Hidden: hidden, Source: source, InFile: s.inBase(a.ID)}
		// Показываем действующее значение, а не то, что лежит в файле: адрес
		// мог быть перекрыт окружением, и правка такого поля ничего не даст.
		if u := os.Getenv(config.AgentEnvVar(a.ID, "URL")); u != "" {
			r.URL = u
			r.EnvLocked = append(r.EnvLocked, "url")
		}
		r.HasPassword = a.Auth.Password != ""
		if os.Getenv(config.AgentEnvVar(a.ID, "PASSWORD")) != "" {
			r.HasPassword = true
			r.EnvLocked = append(r.EnvLocked, "password")
		}
		r.Auth.Password = "" // наружу пароль не уходит никогда
		out = append(out, r)
	}
	for _, b := range s.base {
		if o, ok := byID[b.ID]; ok {
			seen[o.ID] = true
			add(o.AgentConfig, o.Hidden, true)
			continue
		}
		add(b, false, false)
	}
	for _, o := range s.over {
		if seen[o.ID] {
			continue
		}
		add(o.AgentConfig, o.Hidden, true)
	}
	return out
}

// apply — общий хвост всех мутаций: пересчитать, записать файл, зафиксировать
// состояние и позвать подписчика. Пока запись не удалась, состояние прежнее.
func (s *Store) apply(over []Override) error {
	merged, err := s.mergeNormalized(over)
	if err != nil {
		return err
	}
	if err := saveOverlay(s.overlayPath, over); err != nil {
		return err
	}
	s.over, s.merged = over, merged
	// Подписчик зовётся под мьютексом хранилища: так правки доезжают до реестра
	// ровно в том порядке, в каком применялись. Отсюда ограничение — подписчику
	// нельзя ходить обратно в Store, это самоблокировка. Единственный подписчик,
	// Registry.Apply, туда и не ходит: он берёт только свою блокировку.
	if s.onChange != nil {
		s.onChange(slices.Clone(merged))
	}
	return nil
}

// indexOver находит overlay-запись по id.
func indexOver(over []Override, id string) int {
	return slices.IndexFunc(over, func(o Override) bool { return o.ID == id })
}

// inBase отвечает, есть ли у агента версия в базовом (рукописном) списке —
// той, что приезжает из orchestrator.yaml и не может быть удалена, только
// скрыта.
func (s *Store) inBase(id string) bool {
	return slices.ContainsFunc(s.base, func(a config.AgentConfig) bool { return a.ID == id })
}

// known отвечает, знает ли Store такого агента — в базе или в overlay.
func (s *Store) known(id string) bool {
	if s.inBase(id) {
		return true
	}
	return indexOver(s.over, id) >= 0
}

// Create заводит нового агента. Занятый id — ошибка: правка существующего идёт
// через Update, и молча перетереть его нельзя.
func (s *Store) Create(a config.AgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := config.ValidateAgent(a); err != nil {
		return err
	}
	if s.known(a.ID) {
		return fmt.Errorf("%w: %q", ErrExists, a.ID)
	}
	return s.apply(append(slices.Clone(s.over), Override{AgentConfig: a}))
}

// Update заменяет запись агента целиком. Пустой пароль означает «не менять»:
// наружу мы его не отдаём, и форма присылает пустое поле каждый раз, когда
// пароль не трогали. Убрать пароль можно, убрав auth целиком.
//
// Существование id проверяется раньше валидации: в отличие от Create, где id
// принадлежит самой новой записи, здесь id указывает на уже существующего
// агента и обязан быть валиден по построению — если его нет, «нет такого
// агента» и есть содержательная причина отказа, а не валидность чужого ввода.
func (s *Store) Update(id string, a config.AgentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	a.ID = id
	if err := config.ValidateAgent(a); err != nil {
		return err
	}
	// Пароль и адрес, перекрытые окружением, в overlay не пишем: форма
	// показывает действующее (env-) значение, и если переносить его в файл как
	// есть, секрет из переменной окружения осел бы открытым текстом на диске, а
	// адрес конкретного контейнера заморозился бы в конфиге стенда. Слияние
	// всё равно даст env последнее слово — важно только, что уходит на диск.
	if a.Auth.Password == "" && os.Getenv(config.AgentEnvVar(id, "PASSWORD")) == "" {
		a.Auth.Password = s.currentPassword(id)
	}
	if os.Getenv(config.AgentEnvVar(id, "URL")) != "" {
		a.URL = s.currentURL(id)
	}
	over := slices.Clone(s.over)
	if i := indexOver(over, id); i >= 0 {
		over[i] = Override{AgentConfig: a}
	} else {
		over = append(over, Override{AgentConfig: a})
	}
	return s.apply(over)
}

// currentPassword достаёт действующий пароль агента — из overlay, иначе из базы.
func (s *Store) currentPassword(id string) string {
	if i := indexOver(s.over, id); i >= 0 {
		return s.over[i].Auth.Password
	}
	for _, b := range s.base {
		if b.ID == id {
			return b.Auth.Password
		}
	}
	return ""
}

// currentURL достаёт адрес агента, каким он был в overlay/базе ДО текущей
// правки, — из overlay, иначе из базы. Нужен, когда адрес перекрыт
// окружением: писать в файл значение, которое форма показала пользователю
// (оно и есть env-значение), нельзя — иначе стенд-специфичный адрес
// заморозится в overlay навсегда.
func (s *Store) currentURL(id string) string {
	if i := indexOver(s.over, id); i >= 0 {
		return s.over[i].URL
	}
	for _, b := range s.base {
		if b.ID == id {
			return b.URL
		}
	}
	return ""
}

// Delete убирает агента. Заведённый через UI исчезает совсем, пришедший из
// YAML помечается скрытым: базовый файл рукописный, и мы его не трогаем.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	inBase := s.inBase(id)
	over := slices.Clone(s.over)
	i := indexOver(over, id)
	switch {
	case inBase && i >= 0:
		over[i] = Override{AgentConfig: config.AgentConfig{ID: id}, Hidden: true}
	case inBase:
		over = append(over, Override{AgentConfig: config.AgentConfig{ID: id}, Hidden: true})
	default:
		over = slices.Delete(over, i, i+1)
	}
	return s.apply(over)
}

// Reset забывает overlay-запись: агент возвращается к версии из YAML, а
// скрытый — в список. Без базовой версии сбрасывать некуда: агент, целиком
// заведённый через UI, при таком «сбросе» просто исчез бы — это дело Delete,
// а не Reset, и делается через явное подтверждение.
func (s *Store) Reset(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.inBase(id) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	i := indexOver(s.over, id)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	over := slices.Clone(s.over)
	return s.apply(slices.Delete(over, i, i+1))
}
