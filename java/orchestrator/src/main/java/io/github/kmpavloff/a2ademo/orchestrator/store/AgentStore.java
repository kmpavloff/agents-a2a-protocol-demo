package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;
import io.github.kmpavloff.a2ademo.common.config.ConfigLoader;

import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.Consumer;

/**
 * Владеет списком агентов: рукописной базой из orchestrator.yaml и
 * overlay-файлом с правками из UI (порт internal/agentstore).
 *
 * <p>Базовый список остаётся рукописным: в нём живут комментарии, объясняющие
 * каждого агента, и переписывать его машинно значило бы их потерять.
 */
public class AgentStore {

    /** Только из orchestrator.yaml, overlay-записи нет. */
    public static final String SOURCE_FILE = "file";

    /** Есть overlay-запись, сделанная из интерфейса. */
    public static final String SOURCE_UI = "ui";

    /** Агента с таким id нет. Отдельный тип: обработчику нужен 404, а не 400. */
    public static class NotFoundException extends RuntimeException {
        public NotFoundException(String id) {
            super("agent not found: \"" + id + "\"");
        }
    }

    /** Агент с таким id уже есть — 409, а не 400. */
    public static class ExistsException extends RuntimeException {
        public ExistsException(String id) {
            super("agent already exists: \"" + id + "\"");
        }
    }

    /**
     * Запись для экрана настроек: действующий конфиг агента плюс происхождение.
     * Пароль сюда не попадает никогда — только признак, что он задан.
     *
     * @param envLocked поля, перекрытые окружением: "url", "password"
     * @param inFile    есть ли у агента версия в базовом (рукописном) списке.
     *                  {@code source} говорит лишь о наличии overlay-записи, а её
     *                  наличие означает разное для файлового агента с правкой из
     *                  UI (сброс вернёт версию из YAML) и для агента, целиком
     *                  заведённого через UI (базовой версии нет вовсе).
     */
    public record Record(AgentConfig agent, boolean hidden, String source,
                         boolean hasPassword, List<String> envLocked, boolean inFile) {}

    private final Path overlayPath;
    private final Object lock = new Object();

    private final List<AgentConfig> base;
    private List<AgentOverride> over;
    private List<AgentConfig> merged;
    private Consumer<List<AgentConfig>> onChange;

    /**
     * Читает overlay и складывает его с базовым списком. Битый overlay — отказ на
     * старте, а не молчаливое игнорирование: агенты, заведённые через UI, не
     * должны исчезать незаметно.
     */
    public AgentStore(List<AgentConfig> base, Path overlayPath) {
        this.base = List.copyOf(base);
        this.overlayPath = overlayPath;
        this.over = OverlayFile.load(overlayPath);
        this.merged = mergeNormalized(this.over);
    }

    /**
     * Задаёт подписчика, которому уезжает новый список после каждой удачной
     * правки. Через него живой реестр узнаёт об изменениях.
     */
    public void onChange(Consumer<List<AgentConfig>> fn) {
        synchronized (lock) {
            onChange = fn;
        }
    }

    /** Действующий список — тот, по которому живёт реестр. */
    public List<AgentConfig> agents() {
        synchronized (lock) {
            return merged;
        }
    }

    /**
     * Складывает базу с overlay и прогоняет через ту же нормализацию, что и
     * загрузка конфига, — так env-перекрытия остаются последним словом и после
     * правок из UI.
     */
    private List<AgentConfig> mergeNormalized(List<AgentOverride> o) {
        return ConfigLoader.normalizeAgents(Overlays.merge(base, o));
    }

    /**
     * Описывает агентов для экрана настроек, включая скрытых: вернуть удалённого
     * агента иначе можно было бы только правкой файла.
     */
    public List<Record> records() {
        synchronized (lock) {
            Map<String, AgentOverride> byId = new LinkedHashMap<>(over.size());
            for (AgentOverride o : over) {
                byId.put(o.id(), o);
            }
            List<Record> out = new ArrayList<>(base.size() + over.size());
            Set<String> seen = new HashSet<>(over.size());
            for (AgentConfig b : base) {
                AgentOverride o = byId.get(b.id());
                if (o != null) {
                    seen.add(o.id());
                    out.add(record(o.agent(), o.hidden(), true));
                } else {
                    out.add(record(b, false, false));
                }
            }
            for (AgentOverride o : over) {
                if (!seen.contains(o.id())) {
                    out.add(record(o.agent(), o.hidden(), true));
                }
            }
            return out;
        }
    }

    private Record record(AgentConfig a, boolean hidden, boolean fromOverlay) {
        List<String> envLocked = new ArrayList<>(2);
        // Показываем действующее значение, а не то, что лежит в файле: адрес мог
        // быть перекрыт окружением, и правка такого поля ничего не даст.
        String urlEnv = System.getenv(AgentConfig.envVar(a.id(), "URL"));
        if (urlEnv != null && !urlEnv.isBlank()) {
            a = a.withUrl(urlEnv);
            envLocked.add("url");
        }
        boolean hasPassword = !a.auth().password().isEmpty();
        String passEnv = System.getenv(AgentConfig.envVar(a.id(), "PASSWORD"));
        if (passEnv != null && !passEnv.isBlank()) {
            hasPassword = true;
            envLocked.add("password");
        }
        return new Record(a.withPassword(""), hidden, fromOverlay ? SOURCE_UI : SOURCE_FILE,
                hasPassword, List.copyOf(envLocked), inBase(a.id()));
    }

    /**
     * Общий хвост всех мутаций: пересчитать, записать файл, зафиксировать
     * состояние и позвать подписчика. Пока запись не удалась, состояние прежнее.
     */
    private void apply(List<AgentOverride> next) {
        List<AgentConfig> nextMerged = mergeNormalized(next);
        OverlayFile.save(overlayPath, next);
        over = next;
        merged = nextMerged;
        // Подписчик зовётся под блокировкой хранилища: так правки доезжают до
        // реестра ровно в том порядке, в каком применялись. Отсюда ограничение —
        // подписчику нельзя ходить обратно в AgentStore. Единственный подписчик,
        // Registry.apply, туда и не ходит.
        if (onChange != null) {
            onChange.accept(nextMerged);
        }
    }

    private boolean inBase(String id) {
        return base.stream().anyMatch(a -> a.id().equals(id));
    }

    private int indexOver(List<AgentOverride> list, String id) {
        for (int i = 0; i < list.size(); i++) {
            if (list.get(i).id().equals(id)) {
                return i;
            }
        }
        return -1;
    }

    private boolean known(String id) {
        return inBase(id) || indexOver(over, id) >= 0;
    }

    /**
     * Заводит нового агента. Занятый id — ошибка: правка существующего идёт
     * через {@link #update}, и молча перетереть его нельзя.
     */
    public void create(AgentConfig a) {
        synchronized (lock) {
            AgentConfig.validate(a);
            if (known(a.id())) {
                throw new ExistsException(a.id());
            }
            List<AgentOverride> next = new ArrayList<>(over);
            next.add(new AgentOverride(a, false));
            apply(next);
        }
    }

    /**
     * Заменяет запись агента целиком. Пустой пароль означает «не менять».
     *
     * <p>Существование id проверяется раньше валидации: здесь id указывает на уже
     * существующего агента и обязан быть валиден по построению — если его нет,
     * «нет такого агента» и есть содержательная причина отказа.
     */
    public void update(String id, AgentConfig a) {
        synchronized (lock) {
            if (!known(id)) {
                throw new NotFoundException(id);
            }
            a = a.withId(id);
            AgentConfig.validate(a);
            // Пароль и адрес, перекрытые окружением, в overlay не пишем: форма
            // показывает действующее (env-) значение, и если переносить его в
            // файл как есть, секрет из переменной осел бы открытым текстом на
            // диске, а адрес конкретного контейнера заморозился бы в конфиге.
            String passEnv = System.getenv(AgentConfig.envVar(id, "PASSWORD"));
            if (a.auth().password().isEmpty() && (passEnv == null || passEnv.isBlank())) {
                a = a.withPassword(currentPassword(id));
            }
            String urlEnv = System.getenv(AgentConfig.envVar(id, "URL"));
            if (urlEnv != null && !urlEnv.isBlank()) {
                a = a.withUrl(currentUrl(id));
            }
            List<AgentOverride> next = new ArrayList<>(over);
            int i = indexOver(next, id);
            if (i >= 0) {
                next.set(i, new AgentOverride(a, false));
            } else {
                next.add(new AgentOverride(a, false));
            }
            apply(next);
        }
    }

    /** Действующий пароль агента — из overlay, иначе из базы. */
    private String currentPassword(String id) {
        int i = indexOver(over, id);
        if (i >= 0) {
            return over.get(i).agent().auth().password();
        }
        return base.stream().filter(a -> a.id().equals(id)).findFirst()
                .map(a -> a.auth().password()).orElse("");
    }

    /** Адрес агента, каким он был в overlay/базе ДО текущей правки. */
    private String currentUrl(String id) {
        int i = indexOver(over, id);
        if (i >= 0) {
            return over.get(i).agent().url();
        }
        return base.stream().filter(a -> a.id().equals(id)).findFirst().map(AgentConfig::url).orElse("");
    }

    /**
     * Убирает агента. Заведённый через UI исчезает совсем, пришедший из YAML
     * помечается скрытым: базовый файл рукописный, и мы его не трогаем.
     */
    public void delete(String id) {
        synchronized (lock) {
            if (!known(id)) {
                throw new NotFoundException(id);
            }
            List<AgentOverride> next = new ArrayList<>(over);
            int i = indexOver(next, id);
            AgentOverride hidden = new AgentOverride(
                    new AgentConfig(id, "", "", "", "", false, "", "", null), true);
            if (inBase(id)) {
                if (i >= 0) {
                    next.set(i, hidden);
                } else {
                    next.add(hidden);
                }
            } else {
                next.remove(i);
            }
            apply(next);
        }
    }

    /**
     * Забывает overlay-запись: агент возвращается к версии из YAML, а скрытый —
     * в список. Без базовой версии сбрасывать некуда: агент, целиком заведённый
     * через UI, при таком «сбросе» просто исчез бы — это дело {@link #delete}.
     */
    public void reset(String id) {
        synchronized (lock) {
            int i = indexOver(over, id);
            if (!inBase(id) || i < 0) {
                throw new NotFoundException(id);
            }
            List<AgentOverride> next = new ArrayList<>(over);
            next.remove(i);
            apply(next);
        }
    }
}
