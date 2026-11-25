package ui

type Decision string

const (
	Answered          Decision = "answered"
	NeedsConfirmation Decision = "needs_confirmation"
	Blocked           Decision = "blocked"
	Clarify           Decision = "clarify"
	Refused           Decision = "refused"
	Unknown           Decision = "unknown"
)

type Response struct {
	Version string  `json:"version"`
	Blocks  []Block `json:"blocks"`
	Audit   Audit   `json:"audit"`
}

type Audit struct {
	UserID         string   `json:"user_id,omitempty"`
	Intent         string   `json:"intent,omitempty"`
	ToolsCalled    []string `json:"tools_called,omitempty"`
	Decision       Decision `json:"decision"`
	Reason         string   `json:"reason,omitempty"`
	KBDocIDs       []string `json:"kb_doc_ids,omitempty"`
	TokenRejection string   `json:"token_rejection,omitempty"`
}

type Block interface{ isBlock() }

type Text struct {
	Type     string `json:"type"`
	Markdown string `json:"markdown"`
}

type RestaurantCard struct {
	Type           string  `json:"type"`
	RestaurantID   string  `json:"restaurant_id"`
	Name           string  `json:"name"`
	Cuisine        string  `json:"cuisine"`
	Rating         float64 `json:"rating"`
	DeliveryFeeTRY float64 `json:"delivery_fee_try"`
	MinOrderTRY    float64 `json:"min_order_try"`
	ETAMin         int     `json:"eta_min"`
	District       string  `json:"district"`
}

type MenuItem struct {
	Type          string  `json:"type"`
	ItemID        string  `json:"item_id"`
	Name          string  `json:"name"`
	PriceTRY      float64 `json:"price_try"`
	Available     bool    `json:"available"`
	AgeRestricted bool    `json:"age_restricted"`
	Category      string  `json:"category"`
}

type CartSummary struct {
	Type           string     `json:"type"`
	RestaurantID   string     `json:"restaurant_id,omitempty"`
	RestaurantName string     `json:"restaurant_name,omitempty"`
	Items          []CartLine `json:"items"`
	SubtotalTRY    float64    `json:"subtotal_try"`
	DeliveryFeeTRY float64    `json:"delivery_fee_try"`
	TotalTRY       float64    `json:"total_try"`
	MinOrderTRY    float64    `json:"min_order_try"`
	MeetsMinimum   bool       `json:"meets_minimum"`
}

type CartLine struct {
	Name     string  `json:"name"`
	Qty      int     `json:"qty"`
	PriceTRY float64 `json:"price_try"`
}

type OrderSummary struct {
	Type       string   `json:"type"`
	OrderID    string   `json:"order_id"`
	Restaurant string   `json:"restaurant,omitempty"`
	TotalTRY   *float64 `json:"total_try,omitempty"`
	TipTRY     *float64 `json:"tip_try,omitempty"`
	Status     string   `json:"status"`
	Event      string   `json:"event"`
	Refund     *Refund  `json:"refund,omitempty"`
}

type Refund struct {
	ToWalletTRY           float64 `json:"to_wallet_try"`
	ToCardTRY             float64 `json:"to_card_try"`
	OrderByPaymentsSystem bool    `json:"order_by_payments_system,omitempty"`
}

type OrderHistory struct {
	Type   string         `json:"type"`
	Orders []HistoryEntry `json:"orders"`
}

type HistoryEntry struct {
	OrderID    string  `json:"order_id"`
	Date       string  `json:"date"`
	Restaurant string  `json:"restaurant,omitempty"`
	TotalTRY   float64 `json:"total_try"`
	Status     string  `json:"status"`
}

type ConfirmationPrompt struct {
	Type         string         `json:"type"`
	Action       string         `json:"action"`
	Summary      string         `json:"summary"`
	Params       map[string]any `json:"params"`
	ConfirmToken string         `json:"confirm_token"`
	ExpiresAt    string         `json:"expires_at"`
	Assumptions  []string       `json:"assumptions,omitempty"`
	Payment      *Payment       `json:"payment,omitempty"`
}

type Payment struct {
	FromWalletTRY float64 `json:"from_wallet_try"`
	FromCardTRY   float64 `json:"from_card_try"`
}

type VerificationGate struct {
	Type        string `json:"type"`
	Requirement string `json:"requirement"`
	Reason      string `json:"reason"`
	CTA         string `json:"cta"`
}

type SuggestedActions struct {
	Type  string   `json:"type"`
	Chips []string `json:"chips"`
}

type Error struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Citation struct {
	Type     string `json:"type"`
	DocID    string `json:"doc_id"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Date     string `json:"date,omitempty"`
}

func (Text) isBlock()               {}
func (RestaurantCard) isBlock()     {}
func (MenuItem) isBlock()           {}
func (CartSummary) isBlock()        {}
func (OrderSummary) isBlock()       {}
func (OrderHistory) isBlock()       {}
func (ConfirmationPrompt) isBlock() {}
func (VerificationGate) isBlock()   {}
func (SuggestedActions) isBlock()   {}
func (Error) isBlock()              {}
func (Citation) isBlock()           {}
