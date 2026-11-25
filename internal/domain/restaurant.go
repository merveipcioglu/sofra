package domain

import (
	"slices"
	"strings"
)

type (
	RestaurantID string
	ItemID       string
)

type MenuItem struct {
	ID            ItemID
	Name          string
	Price         Money
	Category      string
	Available     bool
	AgeRestricted bool
}

type Restaurant struct {
	ID                RestaurantID
	Name              string
	Cuisine           string
	City              string
	Rating            float64
	DeliveryFee       Money
	MinOrder          Money
	ETAMinutes        int
	District          string
	DeliveryDistricts []string
	Menu              []MenuItem
}

func (r Restaurant) DeliversTo(district string) bool {
	return slices.ContainsFunc(r.DeliveryDistricts, func(d string) bool { return SameDistrict(d, district) })
}

func SameDistrict(a, b string) bool { return foldName(a) == foldName(b) }

var turkishLetters = strings.NewReplacer("ı", "i", "ö", "o", "ü", "u", "ş", "s", "ç", "c", "ğ", "g")

func foldName(s string) string {
	return turkishLetters.Replace(strings.ToLower(strings.TrimSpace(s)))
}

func (r Restaurant) MenuItem(id ItemID) (MenuItem, bool) {
	i := slices.IndexFunc(r.Menu, func(m MenuItem) bool { return m.ID == id })
	if i < 0 {
		return MenuItem{}, false
	}
	return r.Menu[i], true
}
