package tools

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"sofra/internal/action"
	"sofra/internal/domain"
	"sofra/internal/knowledge"
	"sofra/internal/pricing"
	"sofra/internal/store"
	"sofra/internal/ui"
)

type Result struct {
	Content   string
	IsError   bool
	Blocks    []ui.Block
	Outcome   *action.Outcome
	Params    map[string]any
	Knowledge []knowledge.Result
}

type Executor struct {
	store   *store.Store
	kb      *knowledge.Base
	actions *action.Service
}

func NewExecutor(s *store.Store, kb *knowledge.Base, actions *action.Service) *Executor {
	return &Executor{store: s, kb: kb, actions: actions}
}

type call struct {
	user  domain.User
	turn  int
	input []byte
}

type handler func(*Executor, call) (Result, error)

var handlers = map[string]handler{
	"get_user":                 (*Executor).getUser,
	"get_cart":                 (*Executor).getCart,
	"search_restaurants":       (*Executor).searchRestaurants,
	"get_menu":                 (*Executor).getMenu,
	"quote_checkout":           (*Executor).quoteCheckout,
	"list_orders":              (*Executor).listOrders,
	SearchKnowledge:            (*Executor).searchKnowledge,
	string(domain.PlaceOrder):  (*Executor).placeOrder,
	string(domain.CancelOrder): (*Executor).cancelOrder,
	string(domain.AddTip):      (*Executor).addTip,
}

const SearchKnowledge = "search_knowledge"

func Sensitive(name string) bool {
	return domain.Action(name).Valid()
}

func ReadOnly(name string) bool {
	_, known := handlers[name]
	return known && !Sensitive(name)
}

var errInput = errors.New("invalid tool input")

func (e *Executor) Run(userID domain.UserID, turn int, name string, input []byte) (Result, error) {
	h, ok := handlers[name]
	if !ok {
		return errorResult(fmt.Sprintf("unknown tool %q", name)), nil
	}
	user, err := e.store.User(userID)
	if err != nil {
		return Result{}, err
	}
	res, err := h(e, call{user: user, turn: turn, input: input})
	if errors.Is(err, errInput) {
		return errorResult(err.Error()), nil
	}
	return res, err
}

func errorResult(msg string) Result {
	return Result{Content: mustJSON(errorView{Error: msg}), IsError: true}
}

func decode(input []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", errInput, err)
	}
	return nil
}

func mustJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func (e *Executor) getUser(c call) (Result, error) {
	return Result{Content: mustJSON(newUserView(c.user))}, nil
}

func (e *Executor) getCart(c call) (Result, error) {
	cart, err := e.store.Cart(c.user.ID)
	if err != nil {
		return Result{}, err
	}
	if cart.IsEmpty() {
		return Result{Content: mustJSON(emptyCartView{Items: []quoteLineView{}})}, nil
	}
	r, q, err := e.quote(cart.RestaurantID, cart.Items)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: mustJSON(newQuoteView(r, q)), Blocks: []ui.Block{ui.NewCartSummary(r.ID, r.Name, q)}}, nil
}

var dishCuisines = map[string]string{"pizza": "Italian"}

func (e *Executor) searchRestaurants(c call) (Result, error) {
	var in struct {
		Q            *string `json:"q"`
		Cuisine      *string `json:"cuisine"`
		NearDistrict *string `json:"near_district"`
	}
	if err := decode(c.input, &in); err != nil {
		return Result{}, err
	}
	filter := in.NearDistrict != nil && *in.NearDistrict != ""
	district := c.user.District
	if filter {
		district = *in.NearDistrict
		if known := e.knownDistricts(); !slices.ContainsFunc(known, func(d string) bool { return domain.SameDistrict(d, district) }) {
			return Result{}, fmt.Errorf("%w: unknown district %q; known districts: %s", errInput, district, strings.Join(known, ", "))
		}
	}

	var found []domain.Restaurant
	for _, r := range e.store.Restaurants() {
		if matchesCuisine(r, in.Cuisine) && matchesQuery(r, in.Q) && (!filter || r.DeliversTo(district)) {
			found = append(found, r)
		}
	}
	slices.SortFunc(found, func(a, b domain.Restaurant) int {
		return cmp.Or(cmp.Compare(b.Rating, a.Rating), cmp.Compare(a.Name, b.Name))
	})

	rows := make([]restaurantView, 0, len(found))
	blocks := make([]ui.Block, 0, len(found))
	for _, r := range found {
		delivers := r.DeliversTo(district)
		rows = append(rows, newRestaurantView(r, delivers))
		if delivers {
			blocks = append(blocks, ui.NewRestaurantCard(r))
		}
	}
	return Result{Content: mustJSON(restaurantsView{District: district, Restaurants: rows}), Blocks: blocks}, nil
}

func matchesCuisine(r domain.Restaurant, cuisine *string) bool {
	return cuisine == nil || *cuisine == "" || strings.EqualFold(r.Cuisine, *cuisine)
}

func matchesQuery(r domain.Restaurant, q *string) bool {
	if q == nil || *q == "" {
		return true
	}
	term := fold(*q)
	if c, ok := dishCuisines[term]; ok && r.Cuisine == c {
		return true
	}
	if strings.Contains(fold(r.Name), term) || strings.Contains(fold(r.Cuisine), term) {
		return true
	}
	return slices.ContainsFunc(r.Menu, func(m domain.MenuItem) bool { return strings.Contains(fold(m.Name), term) })
}

func (e *Executor) knownDistricts() []string {
	var all []string
	for _, r := range e.store.Restaurants() {
		all = append(all, r.District)
		all = append(all, r.DeliveryDistricts...)
	}
	slices.Sort(all)
	return slices.Compact(all)
}

func fold(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (e *Executor) getMenu(c call) (Result, error) {
	var in struct {
		RestaurantID string `json:"restaurant_id"`
	}
	if err := decode(c.input, &in); err != nil {
		return Result{}, err
	}
	r, err := e.restaurant(in.RestaurantID)
	if err != nil {
		return Result{}, err
	}
	rows := make([]menuItemView, 0, len(r.Menu))
	blocks := make([]ui.Block, 0, len(r.Menu))
	for _, m := range r.Menu {
		rows = append(rows, newMenuItemView(m))
		blocks = append(blocks, ui.NewMenuItem(m))
	}
	return Result{Content: mustJSON(menuView{RestaurantID: r.ID, Name: r.Name, Items: rows}), Blocks: blocks}, nil
}

type lineInput struct {
	ItemID string `json:"item_id"`
	Qty    int    `json:"qty"`
}

func lineItems(in []lineInput) []domain.LineItem {
	out := make([]domain.LineItem, 0, len(in))
	for _, li := range in {
		out = append(out, domain.LineItem{ItemID: domain.ItemID(li.ItemID), Qty: li.Qty})
	}
	return out
}

func (e *Executor) quoteCheckout(c call) (Result, error) {
	var in struct {
		RestaurantID string      `json:"restaurant_id"`
		Items        []lineInput `json:"items"`
	}
	if err := decode(c.input, &in); err != nil {
		return Result{}, err
	}
	r, q, err := e.quote(domain.RestaurantID(in.RestaurantID), lineItems(in.Items))
	if err != nil {
		return Result{}, err
	}
	return Result{Content: mustJSON(newQuoteView(r, q)), Blocks: []ui.Block{ui.NewCartSummary(r.ID, r.Name, q)}}, nil
}

func (e *Executor) quote(id domain.RestaurantID, items []domain.LineItem) (domain.Restaurant, pricing.Quote, error) {
	r, err := e.restaurant(string(id))
	if err != nil {
		return domain.Restaurant{}, pricing.Quote{}, err
	}
	lines := make([]pricing.Line, 0, len(items))
	for _, li := range items {
		m, ok := r.MenuItem(li.ItemID)
		if !ok {
			return domain.Restaurant{}, pricing.Quote{}, fmt.Errorf("%w: item %s is not on the %s menu", errInput, li.ItemID, r.Name)
		}
		lines = append(lines, pricing.Line{Item: m, Qty: li.Qty})
	}
	q, err := pricing.QuoteOrder(r, lines)
	if err != nil {
		return domain.Restaurant{}, pricing.Quote{}, fmt.Errorf("%w: %v", errInput, err)
	}
	return r, q, nil
}

func (e *Executor) restaurant(id string) (domain.Restaurant, error) {
	r, err := e.store.Restaurant(domain.RestaurantID(id))
	if errors.Is(err, store.ErrNotFound) {
		return domain.Restaurant{}, fmt.Errorf("%w: no restaurant %s", errInput, id)
	}
	return r, err
}

func (e *Executor) listOrders(c call) (Result, error) {
	orders, err := e.store.Orders(c.user.ID)
	if err != nil {
		return Result{}, err
	}
	notes := make(map[string]string, len(orders))
	for _, o := range orders {
		notes[string(o.ID)] = o.Note
	}
	history := ui.NewOrderHistory(orders)
	rows := make([]orderView, 0, len(history.Orders))
	for _, h := range history.Orders {
		row := orderView{OrderID: h.OrderID, Date: h.Date, Restaurant: h.Restaurant, TotalTRY: h.TotalTRY, Status: h.Status}
		if note := notes[h.OrderID]; note != "" {
			row.Note = framed(note)
		}
		rows = append(rows, row)
	}
	return Result{Content: mustJSON(ordersView{Orders: rows}), Blocks: []ui.Block{history}}, nil
}

func (e *Executor) searchKnowledge(c call) (Result, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := decode(c.input, &in); err != nil {
		return Result{}, err
	}
	results := e.kb.Search(in.Query)
	if len(results) == 0 {
		return Result{Content: mustJSON(knowledgeView{
			Results: []docView{}, Note: "No relevant document. Say that the information is not available.",
		})}, nil
	}
	docs := make([]docView, 0, len(results))
	for _, r := range results {
		doc := newDocView(r.Document, "authoritative")
		for _, o := range r.Outdated {
			doc.OutdatedVersions = append(doc.OutdatedVersions, newDocView(o, "outdated: no longer valid, never answer from it"))
		}
		docs = append(docs, doc)
	}
	return Result{Content: mustJSON(knowledgeView{Results: docs}), Knowledge: results}, nil
}

func framed(text string) string {
	return "<untrusted_document>" + frameTag.ReplaceAllString(text, "[tag removed]") + "</untrusted_document>"
}

var frameTag = regexp.MustCompile(`(?i)<\s*/?\s*untrusted_document`)

func (e *Executor) placeOrder(c call) (Result, error) {
	var in struct {
		RestaurantID string      `json:"restaurant_id"`
		Items        []lineInput `json:"items"`
		CartMode     *string     `json:"cart_mode"`
		Confirm      bool        `json:"confirm"`
	}
	if err := decode(c.input, &in); err != nil {
		return Result{}, err
	}
	req := action.PlaceOrderRequest{
		UserID: c.user.ID, RestaurantID: domain.RestaurantID(in.RestaurantID), Items: lineItems(in.Items), Turn: c.turn,
	}
	params := map[string]any{"restaurant_id": in.RestaurantID, "items": in.Items}
	if in.CartMode != nil {
		if *in.CartMode != string(action.CartRequestOnly) {
			return Result{}, fmt.Errorf("%w: cart_mode must be request_only or null", errInput)
		}
		req.CartMode, params["cart_mode"] = action.CartRequestOnly, *in.CartMode
	}
	if !in.Confirm {
		o, err := e.actions.PreparePlaceOrder(req)
		return outcomeResult(params, o, err)
	}
	o, err := e.actions.ConfirmPlaceOrderInChat(req)
	return outcomeResult(params, o, err)
}

func (e *Executor) cancelOrder(c call) (Result, error) {
	var in struct {
		OrderID string `json:"order_id"`
		Confirm bool   `json:"confirm"`
	}
	if err := decode(c.input, &in); err != nil {
		return Result{}, err
	}
	req := action.CancelOrderRequest{UserID: c.user.ID, OrderID: domain.OrderID(in.OrderID), Turn: c.turn}
	params := map[string]any{"order_id": in.OrderID}
	if !in.Confirm {
		o, err := e.actions.PrepareCancelOrder(req)
		return outcomeResult(params, o, err)
	}
	o, err := e.actions.ConfirmCancelOrderInChat(req)
	return outcomeResult(params, o, err)
}

func (e *Executor) addTip(c call) (Result, error) {
	var in struct {
		OrderID   string  `json:"order_id"`
		AmountTRY float64 `json:"amount_try"`
		Confirm   bool    `json:"confirm"`
	}
	if err := decode(c.input, &in); err != nil {
		return Result{}, err
	}
	amount, err := domain.ParseTL(in.AmountTRY)
	if err != nil {
		return Result{}, fmt.Errorf("%w: amount_try: %v", errInput, err)
	}
	req := action.AddTipRequest{UserID: c.user.ID, OrderID: domain.OrderID(in.OrderID), Amount: amount, Turn: c.turn}
	params := map[string]any{"order_id": in.OrderID, "amount_try": in.AmountTRY}
	if !in.Confirm {
		o, err := e.actions.PrepareAddTip(req)
		return outcomeResult(params, o, err)
	}
	o, err := e.actions.ConfirmAddTipInChat(req)
	return outcomeResult(params, o, err)
}

func (e *Executor) ConfirmOnScreen(userID domain.UserID, turn int, token string) (Result, error) {
	o, err := e.actions.ConfirmOnScreen(userID, token, turn)
	if err != nil {
		return Result{}, err
	}
	return outcomeResult(requestParams(o.Request), o, nil)
}

func requestParams(req any) map[string]any {
	switch r := req.(type) {
	case action.PlaceOrderRequest:
		items := make([]lineInput, 0, len(r.Items))
		for _, li := range r.Items {
			items = append(items, lineInput{ItemID: string(li.ItemID), Qty: li.Qty})
		}
		p := map[string]any{"restaurant_id": string(r.RestaurantID), "items": items}
		if r.CartMode != action.CartAuto {
			p["cart_mode"] = string(r.CartMode)
		}
		return p
	case action.CancelOrderRequest:
		return map[string]any{"order_id": string(r.OrderID)}
	case action.AddTipRequest:
		return map[string]any{"order_id": string(r.OrderID), "amount_try": r.Amount.TL()}
	default:
		return nil
	}
}

func outcomeResult(params map[string]any, o action.Outcome, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	return Result{Content: mustJSON(newOutcomeView(o, params)), Outcome: &o, Params: params}, nil
}
