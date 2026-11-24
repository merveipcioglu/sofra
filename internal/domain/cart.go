package domain

import (
	"errors"
	"fmt"
	"slices"
)

type LineItem struct {
	ItemID ItemID
	Qty    int
}

type Cart struct {
	RestaurantID RestaurantID
	Items        []LineItem
}

func (c Cart) IsEmpty() bool {
	return len(c.Items) == 0
}

func (c Cart) Validate() error {
	switch {
	case c.IsEmpty() && c.RestaurantID != "":
		return fmt.Errorf("cart has no items but names restaurant %s", c.RestaurantID)
	case !c.IsEmpty() && c.RestaurantID == "":
		return errors.New("cart has items but no restaurant")
	}
	for i, li := range c.Items {
		if li.Qty < 1 {
			return fmt.Errorf("cart item %s has quantity %d, want at least 1", li.ItemID, li.Qty)
		}
		if slices.ContainsFunc(c.Items[:i], func(x LineItem) bool { return x.ItemID == li.ItemID }) {
			return fmt.Errorf("cart lists item %s twice", li.ItemID)
		}
	}
	return nil
}
