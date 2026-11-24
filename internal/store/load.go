package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"sofra/internal/domain"
)

func Load(dataDir string) (*Store, error) {
	var files struct {
		users       usersFile
		restaurants restaurantsFile
		carts       map[string]cartDTO
		orders      map[string][]orderDTO
	}
	for name, dst := range map[string]any{
		"users.json":       &files.users,
		"restaurants.json": &files.restaurants,
		"carts.json":       &files.carts,
		"orders.json":      &files.orders,
	} {
		if err := readJSON(filepath.Join(dataDir, name), dst); err != nil {
			return nil, err
		}
	}

	s := &Store{
		restaurants: make(map[domain.RestaurantID]domain.Restaurant, len(files.restaurants.Restaurants)),
		state: state{
			users:  make(map[domain.UserID]domain.User, len(files.users.Users)),
			carts:  make(map[domain.UserID]domain.Cart, len(files.carts)),
			orders: make(map[domain.UserID][]domain.Order, len(files.orders)),
			tips:   map[domain.UserID][]domain.Tip{},
		},
	}
	var invalid []string
	bad := func(format string, args ...any) { invalid = append(invalid, fmt.Sprintf(format, args...)) }

	for _, dto := range files.restaurants.Restaurants {
		r, err := dto.toDomain()
		switch _, dup := s.restaurants[r.ID]; {
		case err != nil:
			bad("restaurants.json: %s: %v", dto.ID, err)
		case dup:
			bad("restaurants.json: %s appears twice", dto.ID)
		default:
			s.restaurants[r.ID] = r
			s.restaurantIDs = append(s.restaurantIDs, r.ID)
		}
	}
	for _, dto := range files.users.Users {
		u, err := dto.toDomain()
		switch _, dup := s.state.users[u.ID]; {
		case err != nil:
			bad("users.json: %s: %v", dto.ID, err)
		case dup:
			bad("users.json: %s appears twice", dto.ID)
		default:
			s.state.users[u.ID] = u
		}
	}
	for userID, dto := range files.carts {
		c := dto.toDomain()
		if err := s.checkCart(c); err != nil {
			bad("carts.json: %s: %v", userID, err)
			continue
		}
		s.state.carts[domain.UserID(userID)] = c
	}
	for userID, dtos := range files.orders {
		for _, dto := range dtos {
			o, err := dto.toDomain(domain.UserID(userID))
			if err != nil {
				bad("orders.json: %s: %v", userID, err)
				continue
			}
			s.state.orders[o.UserID] = append(s.state.orders[o.UserID], o)
		}
	}
	if len(invalid) > 0 {
		slices.Sort(invalid)
		return nil, fmt.Errorf("invalid data in %s, %d record(s):\n  %s", dataDir, len(invalid), strings.Join(invalid, "\n  "))
	}
	return s, nil
}

func (s *Store) checkCart(c domain.Cart) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.IsEmpty() {
		return nil
	}
	r, ok := s.restaurants[c.RestaurantID]
	if !ok {
		return fmt.Errorf("cart names restaurant %s, which does not exist", c.RestaurantID)
	}
	for _, li := range c.Items {
		if _, ok := r.MenuItem(li.ItemID); !ok {
			return fmt.Errorf("cart item %s is not on the %s menu", li.ItemID, r.Name)
		}
	}
	return nil
}

func readJSON(path string, dst any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

type usersFile struct {
	Users []userDTO `json:"users"`
}

type userDTO struct {
	ID               string  `json:"id"`
	DisplayName      string  `json:"display_name"`
	WalletBalanceTRY float64 `json:"wallet_balance_try"`
	PaymentMethod    bool    `json:"payment_method"`
	AgeVerified      bool    `json:"age_verified"`
	Address          string  `json:"address"`
	District         string  `json:"district"`
}

func (d userDTO) toDomain() (domain.User, error) {
	balance, err := domain.ParseTL(d.WalletBalanceTRY)
	switch {
	case err != nil:
		return domain.User{}, fmt.Errorf("wallet_balance_try: %w", err)
	case balance < 0:
		return domain.User{}, fmt.Errorf("wallet_balance_try %v is negative", balance)
	}
	return domain.User{
		ID:            domain.UserID(d.ID),
		DisplayName:   d.DisplayName,
		WalletBalance: balance,
		HasSavedCard:  d.PaymentMethod,
		AgeVerified:   d.AgeVerified,
		Address:       d.Address,
		District:      d.District,
	}, nil
}

type restaurantsFile struct {
	Restaurants []restaurantDTO `json:"restaurants"`
}

type restaurantDTO struct {
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	Cuisine           string        `json:"cuisine"`
	City              string        `json:"city"`
	Rating            float64       `json:"rating"`
	DeliveryFeeTRY    float64       `json:"delivery_fee_try"`
	MinOrderTRY       float64       `json:"min_order_try"`
	ETAMinutes        int           `json:"eta_min"`
	District          string        `json:"district"`
	DeliveryDistricts []string      `json:"delivery_districts"`
	Menu              []menuItemDTO `json:"menu"`
}

func (d restaurantDTO) toDomain() (domain.Restaurant, error) {
	menu := make([]domain.MenuItem, 0, len(d.Menu))
	for _, m := range d.Menu {
		item, err := m.toDomain()
		if err != nil {
			return domain.Restaurant{}, fmt.Errorf("menu item %s: %w", m.ItemID, err)
		}
		menu = append(menu, item)
	}
	fee, err := domain.ParseTL(d.DeliveryFeeTRY)
	if err != nil {
		return domain.Restaurant{}, fmt.Errorf("delivery_fee_try: %w", err)
	}
	minOrder, err := domain.ParseTL(d.MinOrderTRY)
	if err != nil {
		return domain.Restaurant{}, fmt.Errorf("min_order_try: %w", err)
	}
	return domain.Restaurant{
		ID:                domain.RestaurantID(d.ID),
		Name:              d.Name,
		Cuisine:           d.Cuisine,
		City:              d.City,
		Rating:            d.Rating,
		DeliveryFee:       fee,
		MinOrder:          minOrder,
		ETAMinutes:        d.ETAMinutes,
		District:          d.District,
		DeliveryDistricts: d.DeliveryDistricts,
		Menu:              menu,
	}, nil
}

type menuItemDTO struct {
	ItemID        string  `json:"item_id"`
	Name          string  `json:"name"`
	PriceTRY      float64 `json:"price_try"`
	Category      string  `json:"category"`
	AgeRestricted bool    `json:"age_restricted"`
	Available     bool    `json:"available"`
}

func (d menuItemDTO) toDomain() (domain.MenuItem, error) {
	price, err := domain.ParseTL(d.PriceTRY)
	if err != nil {
		return domain.MenuItem{}, fmt.Errorf("price_try: %w", err)
	}
	return domain.MenuItem{
		ID:            domain.ItemID(d.ItemID),
		Name:          d.Name,
		Price:         price,
		Category:      d.Category,
		Available:     d.Available,
		AgeRestricted: d.AgeRestricted,
	}, nil
}

type cartDTO struct {
	RestaurantID *string       `json:"restaurant_id"`
	Items        []lineItemDTO `json:"items"`
}

type lineItemDTO struct {
	ItemID string `json:"item_id"`
	Qty    int    `json:"qty"`
}

func (d cartDTO) toDomain() domain.Cart {
	var c domain.Cart
	if d.RestaurantID != nil {
		c.RestaurantID = domain.RestaurantID(*d.RestaurantID)
	}
	for _, li := range d.Items {
		c.Items = append(c.Items, domain.LineItem{ItemID: domain.ItemID(li.ItemID), Qty: li.Qty})
	}
	return c
}

type orderDTO struct {
	OrderID    string  `json:"order_id"`
	Date       string  `json:"date"`
	Restaurant string  `json:"restaurant"`
	TotalTRY   float64 `json:"total_try"`
	Status     string  `json:"status"`
	Note       string  `json:"note"`
}

func (d orderDTO) toDomain(userID domain.UserID) (domain.Order, error) {
	date, err := domain.ParseDate(d.Date)
	if err != nil {
		return domain.Order{}, fmt.Errorf("order %q: %w", d.OrderID, err)
	}
	status, err := domain.ParseOrderStatus(d.Status)
	if err != nil {
		return domain.Order{}, fmt.Errorf("order %q: %w", d.OrderID, err)
	}
	total, err := domain.ParseTL(d.TotalTRY)
	if err != nil {
		return domain.Order{}, fmt.Errorf("order %q: total_try: %w", d.OrderID, err)
	}
	return domain.Order{
		ID:             domain.OrderID(d.OrderID),
		UserID:         userID,
		Date:           date,
		RestaurantName: d.Restaurant,
		Total:          total,
		Status:         status,
		Note:           d.Note,
	}, nil
}
