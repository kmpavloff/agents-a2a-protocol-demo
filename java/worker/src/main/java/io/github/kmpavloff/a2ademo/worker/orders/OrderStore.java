package io.github.kmpavloff.a2ademo.worker.orders;

import io.github.kmpavloff.a2ademo.common.Json;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/** In-memory order store seeded from data/orders.json (port of internal/orders.Store). */
public class OrderStore {
    private final Map<String, Order> orders = new LinkedHashMap<>();
    private final Map<String, Order.SalesStat> stats = new LinkedHashMap<>();

    public static OrderStore load(String path) {
        try {
            byte[] b = Files.readAllBytes(Path.of(path));
            Order.SeedFile seed = Json.MAPPER.readValue(b, Order.SeedFile.class);
            OrderStore s = new OrderStore();
            if (seed.orders() != null) {
                for (Order o : seed.orders()) {
                    s.orders.put(o.id(), o);
                }
            }
            if (seed.salesStats() != null) {
                for (Order.SalesStat st : seed.salesStats()) {
                    s.stats.put(st.period(), st);
                }
            }
            return s;
        } catch (IOException e) {
            throw new IllegalStateException("read seed " + path + ": " + e.getMessage(), e);
        }
    }

    public synchronized Optional<Order> get(String id) {
        return Optional.ofNullable(orders.get(id));
    }

    public synchronized List<Order> byCustomer(String customer) {
        List<Order> out = new ArrayList<>();
        for (Order o : orders.values()) {
            if (o.customer().equals(customer)) {
                out.add(o);
            }
        }
        out.sort(Comparator.comparing(Order::created).reversed());
        return out;
    }

    public synchronized Optional<Order.SalesStat> stats(String period) {
        return Optional.ofNullable(stats.get(period));
    }

    public synchronized List<Order> allOrders() {
        return new ArrayList<>(orders.values());
    }

    /** Marks an order refunded. Throws NotFound/NotRefundable mirroring the Go errors. */
    public synchronized Order refund(String id) {
        Order o = orders.get(id);
        if (o == null) {
            throw new NotFoundException(id);
        }
        if (!o.refundable()) {
            throw new NotRefundableException(id);
        }
        Order refunded = o.withStatus("refunded");
        orders.put(id, refunded);
        return refunded;
    }

    public static class NotFoundException extends RuntimeException {
        public NotFoundException(String id) {
            super(id + ": order not found");
        }
    }

    public static class NotRefundableException extends RuntimeException {
        public NotRefundableException(String id) {
            super(id + ": order is not refundable");
        }
    }
}
