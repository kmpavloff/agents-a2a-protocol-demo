package io.github.kmpavloff.a2ademo.common.a2a;

import com.fasterxml.jackson.annotation.JsonInclude;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/** A2A 1.0 AgentCard (subset used by the demo). Served at {@link #WELL_KNOWN_PATH}. */
@JsonInclude(JsonInclude.Include.NON_NULL)
public class AgentCard {
    public static final String WELL_KNOWN_PATH = "/.well-known/agent-card.json";
    public static final String TRANSPORT_JSONRPC = "JSONRPC";
    public static final String PROTOCOL_VERSION = "1.0";

    public String name;
    public String description;
    public String version;
    public List<AgentInterface> supportedInterfaces = new ArrayList<>();
    /** Always serialized (a2a-go marshals capabilities without omitempty). */
    @JsonInclude(JsonInclude.Include.ALWAYS)
    public Map<String, Object> capabilities = Map.of();
    public List<String> defaultInputModes = new ArrayList<>();
    public List<String> defaultOutputModes = new ArrayList<>();
    public List<AgentSkill> skills = new ArrayList<>();

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class AgentInterface {
        public String url;
        public String protocolBinding;
        public String protocolVersion;

        public AgentInterface() {}

        public static AgentInterface jsonrpc(String url) {
            AgentInterface i = new AgentInterface();
            i.url = url;
            i.protocolBinding = TRANSPORT_JSONRPC;
            i.protocolVersion = PROTOCOL_VERSION;
            return i;
        }
    }

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class AgentSkill {
        public String id;
        public String name;
        public String description;
        public List<String> tags;
        public List<String> examples;
    }
}
