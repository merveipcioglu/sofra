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
	"time"

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
	return Result{Content: mustJSON(map[string]string{"error": msg}), IsError: true}
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
	u := c.user
	return Result{Content: mustJSON(map[string]any{
		"wallet_balance_try": ui.TL(u.WalletBalance), "payment_method": u.HasSavedCard,
		"age_verified": u.AgeVerified, "address": u.Address, "district": u.District,
	})}, nil
}

func (e *Executor) getCart(c call) (Result, error) {
	cart, err := e.store.Cart(c.user.ID)
	if err != nil {
		return Result{}, err
	}
	if cart.IsEmpty() {
		return Result{Content: mustJSON(map[string]any{"restaurant_id": nil, "items": []any{}})}, nil
	}
	q, err := e.quote(cart.RestaurantID, cart.Items)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: quoteJSON(cart.RestaurantID, q), Blocks: []ui.Block{ui.NewCartSummary(cart.RestaurantID, q)}}, nil
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

	rows := make([]map[string]any, 0, len(found))
	blocks := make([]ui.Block, 0, len(found))
	for _, r := range found {
		delivers := r.DeliversTo(district)
		rows = append(rows, map[string]any{
			"id": r.ID, "name": r.Name, "cuisine": r.Cuisine, "rating": r.Rating, "district": r.District,
			"delivery_fee_try": ui.TL(r.DeliveryFee), "min_order_try": ui.TL(r.MinOrder), "eta_min": r.ETAMinutes,
			"delivers_to_district": delivers,
		})
		if delivers {
			blocks = append(blocks, ui.NewRestaurantCard(r))
		}
	}
	return Result{Content: mustJSON(map[string]any{"district": district, "restaurants": rows}), Blocks: blocks}, nil
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
	rows := make([]map[string]any, 0, len(r.Menu))
	blocks := make([]ui.Block, 0, len(r.Menu))
	for _, m := range r.Menu {
		rows = append(rows, map[string]any{
			"item_id": m.ID, "name": m.Name, "price_try": ui.TL(m.Price), "category": m.Category,
			"available": m.Available, "age_restricted": m.AgeRestricted,
		})
		blocks = append(blocks, ui.NewMenuItem(m))
	}
	return Result{Content: mustJSON(map[string]any{"restaurant_id": r.ID, "name": r.Name, "items": rows}), Blocks: blocks}, nil
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
	id := domain.RestaurantID(in.RestaurantID)
	q, err := e.quote(id, lineItems(in.Items))
	if err != nil {
		return Result{}, err
	}
	return Result{Content: quoteJSON(id, q), Blocks: []ui.Block{ui.NewCartSummary(id, q)}}, nil
}

func (e *Executor) quote(id domain.RestaurantID, items []domain.LineItem) (pricing.Quote, error) {
	r, err := e.restaurant(string(id))
	if err != nil {
		return pricing.Quote{}, err
	}
	lines := make([]pricing.Line, 0, len(items))
	for _, li := range items {
		m, ok := r.MenuItem(li.ItemID)
		if !ok {
			return pricing.Quote{}, fmt.Errorf("%w: item %s is not on the %s menu", errInput, li.ItemID, r.Name)
		}
		lines = append(lines, pricing.Line{Item: m, Qty: li.Qty})
	}
	q, err := pricing.QuoteOrder(r, lines)
	if err != nil {
		return pricing.Quote{}, fmt.Errorf("%w: %v", errInput, err)
	}
	return q, nil
}

func quoteJSON(id domain.RestaurantID, q pricing.Quote) string {
	lines := make([]map[string]any, 0, len(q.Lines))
	for _, l := range q.Lines {
		lines = append(lines, map[string]any{"item_id": l.ItemID, "name": l.Name, "qty": l.Qty, "unit_price_try": ui.TL(l.UnitPrice)})
	}
	return mustJSON(map[string]any{
		"restaurant_id": id, "items": lines,
		"subtotal_try": ui.TL(q.Subtotal), "delivery_fee_try": ui.TL(q.DeliveryFee), "total_try": ui.TL(q.Total),
		"min_order_try": ui.TL(q.MinOrder), "meets_minimum": q.MeetsMinimum,
		"free_delivery_threshold_try": ui.TL(q.FreeDeliveryThreshold),
		"free_delivery_remaining_try": ui.TL(q.FreeDeliveryShortfall()),
	})
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
	rows := make([]map[string]any, 0, len(history.Orders))
	for _, h := range history.Orders {
		row := map[string]any{"order_id": h.OrderID, "date": h.Date, "restaurant": h.Restaurant, "total_try": h.TotalTRY, "status": h.Status}
		if note := notes[h.OrderID]; note != "" {
			row["note"] = framed(note)
		}
		rows = append(rows, row)
	}
	return Result{Content: mustJSON(map[string]any{"orders": rows}), Blocks: []ui.Block{history}}, nil
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
		return Result{Content: mustJSON(map[string]any{
			"results": []any{}, "note": "No relevant document. Say that the information is not available.",
		})}, nil
	}
	docs := make([]map[string]any, 0, len(results))
	for _, r := range results {
		doc := docJSON(r.Document, "authoritative")
		var outdated []map[string]any
		for _, o := range r.Outdated {
			outdated = append(outdated, docJSON(o, "outdated: no longer valid, never answer from it"))
		}
		if outdated != nil {
			doc["outdated_versions"] = outdated
		}
		docs = append(docs, doc)
	}
	return Result{Content: mustJSON(map[string]any{"results": docs}), Knowledge: results}, nil
}

func docJSON(d knowledge.Document, status string) map[string]any {
	return map[string]any{
		"doc_id": d.ID, "title": d.Title, "category": d.Category, "date": d.Date, "status": status,
		"content": framed(d.Body),
	}
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
		return map[string]any{"order_id": string(r.OrderID), "amount_try": ui.TL(r.Amount)}
	default:
		return nil
	}
}

func outcomeResult(params map[string]any, o action.Outcome, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	view := map[string]any{"status": o.Status}
	if o.Violation != nil {
		view["requirement"], view["reason"] = o.Violation.Requirement, o.Violation.Detail
	}
	if o.Rejection != "" {
		view["confirmation_rejected"] = o.Rejection
		view["note"] = "The confirmation could not be used and nothing was executed. A new confirmation was issued."
		if o.Status == action.Rejected {
			view["note"] = "The confirmation could not be used and nothing was executed. No new confirmation was issued."
		}
	}
	if o.Used != "" {
		view["already_used_for"] = o.Used
	}
	if o.Token != nil {
		view["expires_at"] = o.Token.ExpiresAt.Format(time.RFC3339)
		view["confirm_with_params"] = params
	}
	if o.Quote != nil {
		view["total_try"] = ui.TL(o.Quote.Total)
	}
	if o.Status == action.NeedsConfirmation && o.Action.Spends() {
		view["payment"] = map[string]any{"from_wallet_try": ui.TL(o.Payment.FromWallet), "from_card_try": ui.TL(o.Payment.FromCard)}
	}
	if texts := ui.AssumptionTexts(o); len(texts) > 0 {
		view["assumptions"] = texts
	}
	if r := o.Receipt; r != nil {
		receipt := map[string]any{"event": r.Event, "order_id": r.OrderID, "status": r.OrderStatus}
		if r.Event == action.EventTipAdded {
			receipt["tip_try"] = ui.TL(r.Amount)
		} else {
			receipt["total_try"] = ui.TL(r.Amount)
		}
		if f := r.Refund; f != nil {
			receipt["refund"] = map[string]any{"to_wallet_try": ui.TL(f.ToWallet), "to_card_try": ui.TL(f.ToCard),
				"order_refunded_by_payments_system": f.OrderUnrecorded, "summary": ui.RefundText(*r)}
		}
		view["receipt"] = receipt
	}
	return Result{Content: mustJSON(view), Outcome: &o, Params: params}, nil
}
