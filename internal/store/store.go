package store

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"sofra/internal/domain"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("already exists")
)

type Store struct {
	restaurants   map[domain.RestaurantID]domain.Restaurant
	restaurantIDs []domain.RestaurantID 

	mu    sync.RWMutex
	state state
}

func (s *Store) Restaurant(id domain.RestaurantID) (domain.Restaurant, error) {
	r, ok := s.restaurants[id]
	if !ok {
		return domain.Restaurant{}, fmt.Errorf("restaurant %q: %w", id, ErrNotFound)
	}
	return cloneRestaurant(r), nil
}

func (s *Store) Restaurants() []domain.Restaurant {
	out := make([]domain.Restaurant, 0, len(s.restaurantIDs))
	for _, id := range s.restaurantIDs {
		out = append(out, cloneRestaurant(s.restaurants[id]))
	}
	return out
}

func (s *Store) User(id domain.UserID) (domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.user(id)
}

func (s *Store) Cart(userID domain.UserID) (domain.Cart, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.cart(userID)
}

func (s *Store) Orders(userID domain.UserID) ([]domain.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.ordersOf(userID)
}

func (s *Store) Tips(userID domain.UserID) ([]domain.Tip, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.tipsOf(userID)
}

func (s *Store) Order(userID domain.UserID, orderID domain.OrderID) (domain.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.order(userID, orderID)
}

func (s *Store) Transact(fn func(*Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	working := s.state.clone()
	if err := fn(&Tx{store: s, state: &working}); err != nil {
		return err
	}
	s.state = working
	return nil
}

type Tx struct {
	store *Store
	state *state
}

func (tx *Tx) Restaurant(id domain.RestaurantID) (domain.Restaurant, error) {
	return tx.store.Restaurant(id)
}

func (tx *Tx) User(id domain.UserID) (domain.User, error) { return tx.state.user(id) }

func (tx *Tx) Cart(userID domain.UserID) (domain.Cart, error) { return tx.state.cart(userID) }

func (tx *Tx) Order(userID domain.UserID, orderID domain.OrderID) (domain.Order, error) {
	return tx.state.order(userID, orderID)
}

func (tx *Tx) Tips(userID domain.UserID) ([]domain.Tip, error) { return tx.state.tipsOf(userID) }

func (tx *Tx) SaveUser(u domain.User) error {
	if _, err := tx.state.user(u.ID); err != nil {
		return err
	}
	tx.state.users[u.ID] = u
	return nil
}

func (tx *Tx) SaveCart(userID domain.UserID, c domain.Cart) error {
	if _, err := tx.state.user(userID); err != nil {
		return err
	}
	tx.state.carts[userID] = cloneCart(c)
	return nil
}

func (tx *Tx) CreateOrder(o domain.Order) error {
	if _, err := tx.state.user(o.UserID); err != nil {
		return err
	}
	for _, orders := range tx.state.orders {
		if slices.ContainsFunc(orders, func(x domain.Order) bool { return x.ID == o.ID }) {
			return fmt.Errorf("order %q: %w", o.ID, ErrDuplicate)
		}
	}
	tx.state.orders[o.UserID] = append(tx.state.orders[o.UserID], cloneOrder(o))
	return nil
}

func (tx *Tx) SaveOrder(o domain.Order) error {
	orders := tx.state.orders[o.UserID]
	i := slices.IndexFunc(orders, func(x domain.Order) bool { return x.ID == o.ID })
	if i < 0 {
		return fmt.Errorf("order %q of user %q: %w", o.ID, o.UserID, ErrNotFound)
	}
	orders[i] = cloneOrder(o)
	return nil
}

func (tx *Tx) CreateTip(tip domain.Tip) error {
	if _, err := tx.state.user(tip.UserID); err != nil {
		return err
	}
	for _, tips := range tx.state.tips {
		if slices.ContainsFunc(tips, func(x domain.Tip) bool { return x.ID == tip.ID }) {
			return fmt.Errorf("tip %q: %w", tip.ID, ErrDuplicate)
		}
	}
	tx.state.tips[tip.UserID] = append(tx.state.tips[tip.UserID], tip)
	return nil
}

type state struct {
	users  map[domain.UserID]domain.User
	carts  map[domain.UserID]domain.Cart
	orders map[domain.UserID][]domain.Order
	tips   map[domain.UserID][]domain.Tip
}

func (st state) clone() state {
	carts := make(map[domain.UserID]domain.Cart, len(st.carts))
	for id, c := range st.carts {
		carts[id] = cloneCart(c)
	}
	orders := make(map[domain.UserID][]domain.Order, len(st.orders))
	for id, list := range st.orders {
		orders[id] = cloneOrders(list)
	}
	tips := make(map[domain.UserID][]domain.Tip, len(st.tips))
	for id, list := range st.tips {
		tips[id] = slices.Clone(list)
	}
	return state{users: maps.Clone(st.users), carts: carts, orders: orders, tips: tips}
}

func (st state) user(id domain.UserID) (domain.User, error) {
	u, ok := st.users[id]
	if !ok {
		return domain.User{}, fmt.Errorf("user %q: %w", id, ErrNotFound)
	}
	return u, nil
}

func (st state) cart(userID domain.UserID) (domain.Cart, error) {
	if _, err := st.user(userID); err != nil {
		return domain.Cart{}, err
	}
	return cloneCart(st.carts[userID]), nil
}

func (st state) ordersOf(userID domain.UserID) ([]domain.Order, error) {
	if _, err := st.user(userID); err != nil {
		return nil, err
	}
	return cloneOrders(st.orders[userID]), nil
}

func (st state) tipsOf(userID domain.UserID) ([]domain.Tip, error) {
	if _, err := st.user(userID); err != nil {
		return nil, err
	}
	return slices.Clone(st.tips[userID]), nil
}

func (st state) order(userID domain.UserID, orderID domain.OrderID) (domain.Order, error) {
	for _, o := range st.orders[userID] {
		if o.ID == orderID {
			return cloneOrder(o), nil
		}
	}
	return domain.Order{}, fmt.Errorf("order %q of user %q: %w", orderID, userID, ErrNotFound)
}

func cloneOrder(o domain.Order) domain.Order {
	o.Items = slices.Clone(o.Items)
	if o.Paid != nil {
		paid := *o.Paid
		o.Paid = &paid
	}
	return o
}

func cloneOrders(list []domain.Order) []domain.Order {
	out := make([]domain.Order, len(list))
	for i, o := range list {
		out[i] = cloneOrder(o)
	}
	return out
}

func cloneCart(c domain.Cart) domain.Cart {
	c.Items = slices.Clone(c.Items)
	return c
}

func cloneRestaurant(r domain.Restaurant) domain.Restaurant {
	r.DeliveryDistricts = slices.Clone(r.DeliveryDistricts)
	r.Menu = slices.Clone(r.Menu)
	return r
}
