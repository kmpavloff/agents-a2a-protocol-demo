package io.github.kmpavloff.a2ademo.worker.orders;

import com.fasterxml.jackson.annotation.JsonProperty;

/** One mock order from data/orders.json. */
public record Order(
        String id,
        String customer,
        String item,
        double amount,
        String currency,
        String status,
        String created,
        boolean refundable) {

    public Order withStatus(String newStatus) {
        return new Order(id, customer, item, amount, currency, newStatus, created, refundable);
    }

    public record SalesStat(String period, int orders, double revenue, String currency) {}

    public record SeedFile(
            @JsonProperty("orders") java.util.List<Order> orders,
            @JsonProperty("sales_stats") java.util.List<SalesStat> salesStats) {}
}
