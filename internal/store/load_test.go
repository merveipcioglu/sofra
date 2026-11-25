package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRefusesInvalidRecords(t *testing.T) {
	cart := func(c string) map[string]string {
		return map[string]string{"carts.json": `{"u_ok": ` + c + `}`}
	}
	edit := func(t *testing.T, file, old, new string) map[string]string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "..", "data", file))
		if err != nil {
			t.Fatal(err)
		}
		broken := strings.Replace(string(raw), old, new, 1)
		if broken == string(raw) {
			t.Fatalf("%s not found in %s", old, file)
		}
		return map[string]string{file: broken}
	}
	cases := []struct {
		name    string
		files   func(t *testing.T) map[string]string
		mention []string
	}{
		{"cart: items but no restaurant", func(*testing.T) map[string]string {
			return cart(`{"restaurant_id": null, "items": [{"item_id": "itm_014", "qty": 1}]}`)
		}, []string{"carts.json", "u_ok", "no restaurant"}},
		{"cart: restaurant but no items", func(*testing.T) map[string]string {
			return cart(`{"restaurant_id": "rst_04", "items": []}`)
		}, []string{"u_ok", "no items"}},
		{"cart: quantity below one", func(*testing.T) map[string]string {
			return cart(`{"restaurant_id": "rst_04", "items": [{"item_id": "itm_014", "qty": 0}]}`)
		}, []string{"u_ok", "at least 1"}},
		{"cart: item listed twice", func(*testing.T) map[string]string {
			return cart(`{"restaurant_id": "rst_04", "items": [{"item_id": "itm_014", "qty": 1}, {"item_id": "itm_014", "qty": 1}]}`)
		}, []string{"u_ok", "twice"}},
		{"cart: unknown restaurant", func(*testing.T) map[string]string {
			return cart(`{"restaurant_id": "rst_99", "items": [{"item_id": "itm_014", "qty": 1}]}`)
		}, []string{"u_ok", "does not exist"}},
		{"cart: item not on the menu", func(*testing.T) map[string]string {
			return cart(`{"restaurant_id": "rst_04", "items": [{"item_id": "itm_001", "qty": 1}]}`)
		}, []string{"u_ok", "not on the"}},
		{"a negative wallet", func(t *testing.T) map[string]string {
			return edit(t, "users.json", `"wallet_balance_try": 800`, `"wallet_balance_try": -100`)
		}, []string{"users.json", "u_ok", "negative"}},
		{"a wallet out of range", func(t *testing.T) map[string]string {
			return edit(t, "users.json", `"wallet_balance_try": 800`, `"wallet_balance_try": 1e20`)
		}, []string{"u_ok", "wallet_balance_try"}},
		{"a user id repeated", func(t *testing.T) map[string]string {
			return edit(t, "users.json", `"id": "u_lowbalance"`, `"id": "u_ok"`)
		}, []string{"u_ok appears twice"}},
		{"an order in an unknown status", func(t *testing.T) map[string]string {
			return edit(t, "orders.json", `"status": "received"`, `"status": "preparing"`)
		}, []string{"orders.json", "u_ok_o9"}},
		{"an order with an unreadable date", func(t *testing.T) map[string]string {
			return edit(t, "orders.json", `"date": "2026-08-19"`, `"date": "2026-13-45"`)
		}, []string{"u_ok_o9"}},
		{"a menu price finer than a kuruş", func(t *testing.T) map[string]string {
			return edit(t, "restaurants.json", `"price_try": 180,`, `"price_try": 180.001,`)
		}, []string{"restaurants.json", "rst_01", "itm_001"}},
		{"a delivery fee out of range", func(t *testing.T) map[string]string {
			return edit(t, "restaurants.json", `"delivery_fee_try": 15,`, `"delivery_fee_try": 1e20,`)
		}, []string{"rst_01", "delivery_fee_try"}},
		{"a restaurant id repeated", func(t *testing.T) map[string]string {
			return edit(t, "restaurants.json", `"id": "rst_02"`, `"id": "rst_01"`)
		}, []string{"rst_01 appears twice"}},
		{"a file that cannot be parsed", func(*testing.T) map[string]string {
			return map[string]string{"orders.json": "{"}
		}, []string{"orders.json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := Load(dataWith(t, c.files(t)))
			if err == nil {
				t.Fatalf("Load started on invalid data (%d restaurants)", len(s.Restaurants()))
			}
			for _, m := range c.mention {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("error %q does not name %q", err, m)
				}
			}
		})
	}
}

func TestLoadReportsEveryInvalidRecord(t *testing.T) {
	users, err := os.ReadFile(filepath.Join("..", "..", "data", "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	broken := map[string]string{
		"users.json": strings.Replace(string(users), `"wallet_balance_try": 800`, `"wallet_balance_try": -100`, 1),
		"carts.json": `{"u_new": {"restaurant_id": "rst_99", "items": [{"item_id": "itm_001", "qty": 1}]}}`,
	}
	_, err = Load(dataWith(t, broken))
	if err == nil || !strings.Contains(err.Error(), "2 record(s)") ||
		!strings.Contains(err.Error(), "u_ok") || !strings.Contains(err.Error(), "u_new") {
		t.Errorf("error %v, want both invalid records named", err)
	}
	if _, err := Load("../../data"); err != nil {
		t.Errorf("the shipped data was refused: %v", err)
	}
}

func dataWith(t *testing.T, replaced map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"users.json", "restaurants.json", "carts.json", "orders.json"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "data", name))
		if err != nil {
			t.Fatal(err)
		}
		if content, ok := replaced[name]; ok {
			raw = []byte(content)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
