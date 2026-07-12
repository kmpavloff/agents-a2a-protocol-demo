package io.github.kmpavloff.a2ademo.common.a2a;

/** A2A 1.0 task state string constants (proto enum names, as serialized by a2a-go v2). */
public final class TaskState {
    public static final String SUBMITTED = "TASK_STATE_SUBMITTED";
    public static final String WORKING = "TASK_STATE_WORKING";
    public static final String INPUT_REQUIRED = "TASK_STATE_INPUT_REQUIRED";
    public static final String COMPLETED = "TASK_STATE_COMPLETED";
    public static final String CANCELED = "TASK_STATE_CANCELED";
    public static final String FAILED = "TASK_STATE_FAILED";

    private TaskState() {}
}
