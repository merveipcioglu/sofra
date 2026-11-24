package tools

import (
	"fmt"

	"sofra/internal/domain"
	"sofra/internal/policy"
)

type Definition struct {
	Name        string
	Description string
	Properties  map[string]any
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func nullable(schema map[string]any) map[string]any {
	return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
}

var itemsSchema = map[string]any{
	"type": "array",
	"items": map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"item_id", "qty"},
		"properties": map[string]any{
			"item_id": map[string]any{"type": "string"},
			"qty":     map[string]any{"type": "integer", "description": fmt.Sprintf("From 1 to %d. Ordering cannot remove items from the cart.", policy.MaxItemQty)},
		},
	},
}

var confirmParam = map[string]any{"type": "boolean",
	"description": "true only when the user's latest message explicitly confirms; false otherwise."}

const sensitive = ` With confirm false, it checks every rule and either explains why the
action is blocked or returns a confirmation for the user; nothing is executed.
With confirm true, it executes only if the user's pending confirmation is for
exactly these parameters; otherwise it executes nothing and issues a new
confirmation. Set confirm true only when the user's latest message explicitly
confirms, with the parameters in confirm_with_params.`

var tipAmount = fmt.Sprintf("Tip in TL, more than 0. The tips on one order total at most the order's total or %v, whichever is lower.",
	policy.TipCap)

func Definitions() []Definition {
	return []Definition{
		{Name: "get_user", Description: "The signed-in user's wallet balance, whether a card is saved, age verification, address and district.",
			Properties: map[string]any{}},
		{Name: "get_cart", Description: "The user's current cart, priced by the checkout rules.",
			Properties: map[string]any{}},
		{Name: "search_restaurants",
			Description: "Restaurants, best rated first. q matches restaurant names, cuisines and dishes; a dish like pizza matches the cuisine that serves it. With near_district, only restaurants that deliver to that district are returned. Without it, every match is returned, and delivers_to_district says whether it delivers to the user's own district.",
			Properties: map[string]any{
				"q":             nullable(str("Free-text search, e.g. a dish or a name.")),
				"cuisine":       nullable(str("Exact cuisine, e.g. Italian.")),
				"near_district": nullable(str("Only restaurants that deliver to this district; null for no delivery filter.")),
			}},
		{Name: "get_menu", Description: "A restaurant's menu: item ids, prices, availability and age restrictions.",
			Properties: map[string]any{"restaurant_id": str("e.g. rst_04")}},
		{Name: "quote_checkout", Description: "Prices a basket at a restaurant: subtotal, delivery fee, total, minimum. The only source of order totals.",
			Properties: map[string]any{"restaurant_id": str("e.g. rst_04"), "items": itemsSchema}},
		{Name: "list_orders", Description: "The user's past and active orders, newest first.",
			Properties: map[string]any{}},
		{Name: SearchKnowledge,
			Description: "Searches Sofra's current policies, FAQs and how-tos. Returns no documents when nothing relevant exists.",
			Properties: map[string]any{"query": str("Two to four English words naming the topic, e.g. \"card refund\" or \"tip commission\". " +
				"Leave out question words and filler such as \"how long\", \"time\" or \"processing\": a document is returned only if it contains most of the query's words.")}},
		{Name: string(domain.PlaceOrder),
			Description: "Places an order. items may be empty to order the cart as it is, and should be empty when the user names a restaurant but no items: the call then checks whether this restaurant can be ordered from (delivery area first) before you ask what to order. If the cart holds items from the same restaurant they are included, unless cart_mode is request_only." + sensitive,
			Properties: map[string]any{
				"restaurant_id": str("e.g. rst_04"),
				"items":         itemsSchema,
				"cart_mode":     nullable(map[string]any{"type": "string", "enum": []string{"request_only"}, "description": "Only when the user explicitly asks to order just the named items."}),
				"confirm":       confirmParam,
			}},
		{Name: string(domain.CancelOrder), Description: "Cancels an order that the restaurant has not started preparing." + sensitive,
			Properties: map[string]any{
				"order_id": str("e.g. u_ok_o9"),
				"confirm":  confirmParam,
			}},
		{Name: string(domain.AddTip), Description: "Leaves the courier a tip on an order. A second tip adds to the first." + sensitive,
			Properties: map[string]any{
				"order_id":   str("e.g. u_ok_o9"),
				"amount_try": map[string]any{"type": "number", "description": tipAmount},
				"confirm":    confirmParam,
			}},
	}
}
