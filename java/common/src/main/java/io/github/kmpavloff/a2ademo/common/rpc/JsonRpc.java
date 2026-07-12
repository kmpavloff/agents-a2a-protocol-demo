package io.github.kmpavloff.a2ademo.common.rpc;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.databind.JsonNode;

/** JSON-RPC 2.0 envelope types for the A2A JSONRPC binding. */
public final class JsonRpc {
    public static final String VERSION = "2.0";

    /** A2A 1.0 JSON-RPC method names (as used by a2a-go v2). */
    public static final String METHOD_SEND_MESSAGE = "SendMessage";
    public static final String METHOD_GET_TASK = "GetTask";
    public static final String METHOD_CANCEL_TASK = "CancelTask";

    public static final int CODE_PARSE_ERROR = -32700;
    public static final int CODE_INVALID_REQUEST = -32600;
    public static final int CODE_METHOD_NOT_FOUND = -32601;
    public static final int CODE_TASK_NOT_FOUND = -32001;

    private JsonRpc() {}

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class Request {
        public String jsonrpc = VERSION;
        public JsonNode id;
        public String method;
        public JsonNode params;
    }

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class Response {
        public String jsonrpc = VERSION;
        public JsonNode id;
        public Object result;
        public Error error;

        public static Response ok(JsonNode id, Object result) {
            Response r = new Response();
            r.id = id;
            r.result = result;
            return r;
        }

        public static Response fail(JsonNode id, int code, String message) {
            Response r = new Response();
            r.id = id;
            r.error = new Error();
            r.error.code = code;
            r.error.message = message;
            return r;
        }
    }

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public static class Error {
        public int code;
        public String message;
        public JsonNode data;
    }
}
