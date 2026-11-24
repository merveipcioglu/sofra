package confirm

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"sofra/internal/clock"
	"sofra/internal/domain"
)

const (
	nonceSize     = 16
	minSecretSize = 32
)

type Reason string

const (
	Malformed      Reason = "malformed"
	Unknown        Reason = "unknown"
	BadSignature   Reason = "bad_signature"
	WrongUser      Reason = "wrong_user"
	AlreadyUsed    Reason = "already_used"
	Superseded     Reason = "superseded"
	Expired        Reason = "expired"
	SameTurn       Reason = "same_turn"
	ParamsMismatch Reason = "params_mismatch"
	StateChanged   Reason = "state_changed"
)

type RejectedError struct {
	Reason Reason
}

func (e *RejectedError) Error() string { return "approval rejected: " + string(e.Reason) }

func ReasonOf(err error) (Reason, bool) {
	var rejected *RejectedError
	if errors.As(err, &rejected) {
		return rejected.Reason, true
	}
	return "", false
}

type Token struct {
	Value     string
	ExpiresAt time.Time
}

type Approved struct {
	Request Params
	Resolved Params
	Snapshot any
}

type Approval struct {
	nonce    [nonceSize]byte
	action   domain.Action
	request  [sha256.Size]byte
	resolved [sha256.Size]byte
	snapshot any
}

func (a Approval) Action() domain.Action { return a.action }

func (a Approval) Snapshot() any { return a.snapshot }

func (a Approval) AsksFor(p Params) bool { return p.action == a.action && p.hash() == a.request }

func (a Approval) Costs(p Params) bool { return p.action == a.action && p.hash() == a.resolved }

func (a Approval) DerivedID(prefix string) string {
	return prefix + hex.EncodeToString(a.nonce[:])
}

type status int

const (
	pending status = iota
	consumed
	revoked
)

type record struct {
	userID     domain.UserID
	approval   Approval
	expiresAt  time.Time
	issuedTurn int
	status     status
}

type userAction struct {
	userID domain.UserID
	action domain.Action
}


type Service struct {
	secret []byte
	clock  clock.Clock

	mu      sync.Mutex
	records map[[nonceSize]byte]*record
	latest map[userAction]string
}

func NewService(secret []byte, clk clock.Clock) (*Service, error) {
	if len(secret) < minSecretSize {
		return nil, fmt.Errorf("token secret must be at least %d bytes", minSecretSize)
	}
	return &Service{secret: secret, clock: clk, records: map[[nonceSize]byte]*record{}, latest: map[userAction]string{}}, nil
}

func RandomSecret() ([]byte, error) {
	secret := make([]byte, minSecretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate token secret: %w", err)
	}
	return secret, nil
}

func (s *Service) Issue(userID domain.UserID, a Approved, ttl time.Duration, turn int) (Token, error) {
	if a.Request.action != a.Resolved.action {
		return Token{}, fmt.Errorf("approval for %s resolved as %s", a.Request.action, a.Resolved.action)
	}
	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Token{}, fmt.Errorf("generate nonce: %w", err)
	}
	rec := &record{
		userID: userID,
		approval: Approval{
			nonce: nonce, action: a.Request.action,
			request: a.Request.hash(), resolved: a.Resolved.hash(), snapshot: a.Snapshot,
		},
		expiresAt:  s.clock.Now().Add(ttl),
		issuedTurn: turn,
		status:     pending,
	}
	signature := s.sign(rec)

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, other := range s.records {
		if other.status == pending && other.userID == userID && other.approval.action == rec.approval.action {
			other.status = revoked
		}
	}
	s.records[nonce] = rec
	value := encode(nonce[:]) + "." + encode(signature)
	s.latest[userAction{userID, rec.approval.action}] = value
	return Token{Value: value, ExpiresAt: rec.expiresAt}, nil
}

func (s *Service) Verify(userID domain.UserID, token string, turn int) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verify(userID, token, turn)
}

func (s *Service) Latest(userID domain.UserID, action domain.Action, turn int) (Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.latest[userAction{userID, action}]
	if !ok {
		return Approval{}, &RejectedError{Unknown}
	}
	return s.verify(userID, token, turn)
}

func (s *Service) verify(userID domain.UserID, token string, turn int) (Approval, error) {
	nonce, signature, ok := decode(token)
	if !ok {
		return Approval{}, &RejectedError{Malformed}
	}
	rec, ok := s.records[nonce]
	if !ok {
		return Approval{}, &RejectedError{Unknown}
	}

	if !hmac.Equal(signature, s.sign(rec)) {
		return Approval{}, &RejectedError{BadSignature}
	}
	if rec.userID != userID {
		return Approval{}, &RejectedError{WrongUser}
	}
	switch rec.status {
	case consumed:
		return rec.approval, &RejectedError{AlreadyUsed}
	case revoked:
		return rec.approval, &RejectedError{Superseded}
	}
	if !s.clock.Now().Before(rec.expiresAt) {
		return rec.approval, &RejectedError{Expired}
	}
	if turn <= rec.issuedTurn {
		
		return rec.approval, &RejectedError{SameTurn}
	}
	return rec.approval, nil
}

func (s *Service) Consume(a Approval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[a.nonce]
	switch {
	case !ok:
		return &RejectedError{Unknown}
	case rec.status == consumed:
		return &RejectedError{AlreadyUsed}
	case rec.status == revoked:
		return &RejectedError{Superseded}
	}
	rec.status = consumed
	return nil
}


func (s *Service) sign(rec *record) []byte {
	a := rec.approval
	payload, _ := json.Marshal(struct { 
		UserID       domain.UserID `json:"user_id"`
		Action       domain.Action `json:"action"`
		RequestHash  string        `json:"request_hash"`
		ResolvedHash string        `json:"resolved_hash"`
		ExpiresAt    int64         `json:"expires_at"`
		Nonce        string        `json:"nonce"`
	}{
		UserID:       rec.userID,
		Action:       a.action,
		RequestHash:  hex.EncodeToString(a.request[:]),
		ResolvedHash: hex.EncodeToString(a.resolved[:]),
		ExpiresAt:    rec.expiresAt.UnixNano(),
		Nonce:        hex.EncodeToString(a.nonce[:]),
	})
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

func encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decode(token string) (nonce [nonceSize]byte, signature []byte, ok bool) {
	noncePart, sigPart, found := strings.Cut(token, ".")
	if !found {
		return nonce, nil, false
	}
	n, err := base64.RawURLEncoding.DecodeString(noncePart)
	if err != nil || len(n) != nonceSize {
		return nonce, nil, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil || len(sig) != sha256.Size {
		return nonce, nil, false
	}
	copy(nonce[:], n)
	return nonce, sig, true
}
