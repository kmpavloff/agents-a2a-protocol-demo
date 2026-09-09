package io.github.kmpavloff.a2ademo.orchestrator.store;

import io.github.kmpavloff.a2ademo.common.config.AgentConfig;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** Слияние overlay-правок с базовым списком агентов. */
public final class Overlays {

    private Overlays() {}

    /**
     * Накладывает overlay на базовый список.
     *
     * <p>Запись с совпавшим id заменяет базовую целиком и остаётся на её месте:
     * порядок задаёт пункты селектора в браузере и выбор агента для
     * терминального REPL, и менять его из-за правки адреса нельзя. Записи с
     * незнакомыми id — это заведённые через UI агенты, они уходят в конец в
     * порядке overlay.
     */
    public static List<AgentConfig> merge(List<AgentConfig> base, List<AgentOverride> over) {
        Map<String, AgentOverride> byId = new LinkedHashMap<>(over.size());
        for (AgentOverride o : over) {
            byId.put(o.id(), o);
        }
        Set<String> used = new HashSet<>(over.size());
        List<AgentConfig> out = new ArrayList<>(base.size() + over.size());
        for (AgentConfig b : base) {
            AgentOverride o = byId.get(b.id());
            if (o == null) {
                out.add(b);
                continue;
            }
            used.add(o.id());
            if (!o.hidden()) {
                out.add(o.agent());
            }
        }
        for (AgentOverride o : over) {
            if (!used.contains(o.id()) && !o.hidden()) {
                out.add(o.agent());
            }
        }
        return out;
    }
}
