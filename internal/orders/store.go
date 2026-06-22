// Package orders is the mock order-management domain used by the worker agent.
package orders

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

var (
	ErrNotFound      = errors.New("order not found")
	ErrNotRefundable = errors.New("order is not refundable")
)

type Order struct {
	ID         string  `json:"id"`
	Customer   string  `json:"customer"`
	Item       string  `json:"item"`
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	Status     string  `json:"status"`
	Created    string  `json:"created"`
	Refundable bool    `json:"refundable"`
}

type SalesStat struct {
	Period   string  `json:"period"`
	Orders   int     `json:"orders"`
	Revenue  float64 `json:"revenue"`
	Currency string  `json:"currency"`
}

type Store struct {
	mu     sync.RWMutex
	orders map[string]Order
	stats  map[string]SalesStat
}

type seedFile struct {
	Orders     []Order     `json:"orders"`
	SalesStats []SalesStat `json:"sales_stats"`
}

func Load(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read seed %s: %w", path, err)
	}
	var sf seedFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return nil, fmt.Errorf("parse seed %s: %w", path, err)
	}
	s := &Store{orders: map[string]Order{}, stats: map[string]SalesStat{}}
	for _, o := range sf.Orders {
		s.orders[o.ID] = o
	}
	for _, st := range sf.SalesStats {
		s.stats[st.Period] = st
	}
	return s, nil
}

func (s *Store) Get(id string) (Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	return o, ok
}

func (s *Store) ByCustomer(customer string) []Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Order
	for _, o := range s.orders {
		if o.Customer == customer {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}

func (s *Store) Stats(period string) (SalesStat, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.stats[period]
	return st, ok
}

func (s *Store) AllOrders() []Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Order
	for _, o := range s.orders {
		out = append(out, o)
	}
	return out
}

func (s *Store) Refund(id string) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return Order{}, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	if !o.Refundable {
		return Order{}, fmt.Errorf("%s: %w", id, ErrNotRefundable)
	}
	o.Status = "refunded"
	s.orders[id] = o
	return o, nil
}
