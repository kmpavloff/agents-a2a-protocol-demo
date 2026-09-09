package io.github.kmpavloff.a2ademo.orchestrator.web;

import io.github.kmpavloff.a2ademo.orchestrator.a2a.A2aClient;
import io.github.kmpavloff.a2ademo.orchestrator.a2a.Remote;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/** Решения исполнителя, принимаемые за ход: что показать и как назвать сбой. */
class ExecutorChoicesTest {

    // Промпт запрещает модели называть значения — их покажет карточка. Но если
    // карточки не будет, подводка «Вот детали вашего заказа:» бесполезна:
    // данные остались только в ответе агента.
    @Test
    void keepsTheModelsLeadInWhenAWidgetWillBeShown() {
        assertEquals("Вот детали вашего заказа:",
                OrchestratorWebExecutor.pickAnswer("Вот детали вашего заказа:",
                        List.of("Заказ 1041: наушники, доставлен, 4990 ₽"), true));
    }

    @Test
    void fallsBackToTheAgentAnswerWhenThereWillBeNoWidget() {
        assertEquals("Заказ 1041: наушники, доставлен, 4990 ₽",
                OrchestratorWebExecutor.pickAnswer("Вот детали вашего заказа:",
                        List.of("Заказ 1041: наушники, доставлен, 4990 ₽"), false));
    }

    @Test
    void keepsTheModelsOwnDetailedAnswer() {
        String detailed = "Заказ 1041 — наушники, доставлен 3 сентября, сумма 4990 ₽.";
        assertEquals(detailed, OrchestratorWebExecutor.pickAnswer(detailed, List.of("ок"), false));
    }

    // Провалившийся ход и недоступный агент — разные беды: «недоступен»
    // отправит пользователя чинить сеть там, где агент на связи.
    @Test
    void tellsAFailedTurnApartFromAnUnreachableAgent() {
        assertEquals("Агент \"Ouroboros\" не смог выполнить запрос: timed out",
                OrchestratorWebExecutor.agentErrorText("Ouroboros",
                        new Remote.TurnFailedException("ouroboros", "TASK_STATE_FAILED", "timed out\nсм. docs")));
        assertTrue(OrchestratorWebExecutor.agentErrorText("Ouroboros",
                        new A2aClient.A2aException("connection refused")).contains("недоступен"));
    }

    // Наши HITL-кнопки имеют канонические ответы; чужая кнопка описывается
    // вместе с контекстом — иначе модель видит «нажал return_order» и не знает,
    // какой заказ.
    @Test
    void describesAForeignButtonWithItsContext() {
        assertEquals("да", OrchestratorWebExecutor.actionToPrompt("approve_refund", Map.of()));
        assertEquals("нет", OrchestratorWebExecutor.actionToPrompt("decline_refund", Map.of()));
        assertEquals("Пользователь нажал кнопку «Вернуть заказ» (order_id: 1041)",
                OrchestratorWebExecutor.actionToPrompt("return_order",
                        Map.of("label", "Вернуть заказ", "order_id", "1041")));
    }
}
