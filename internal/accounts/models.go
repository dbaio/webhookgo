package accounts

import "time"

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User is an account. The hash and the disabled mark never reach the API.
type User struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         string     `json:"role"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	HasPassword  bool       `json:"has_password"` // false for those who only sign in with Google
	DisabledAt   *time.Time `json:"-"`
	PasswordHash string     `json:"-"`
}

// Disabled reports whether the account may not sign in.
func (u *User) Disabled() bool { return u.DisabledAt != nil }

// Kinds of one-time links.
const (
	LinkSetup  = "setup"  // creates the first admin
	LinkInvite = "invite" // creates a user with the link's email and role
	LinkReset  = "reset"  // sets a new password for the link's user
)

// Link is what a one-time link grants, as shown before it is redeemed.
type Link struct {
	Kind      string
	Email     string // the invited address, or the user's for a reset; "" for setup
	Role      string
	UserID    string // reset only
	ExpiresAt time.Time
}

// NewLink describes a link to create.
type NewLink struct {
	Kind      string
	Email     string // invite
	Role      string // invite; setup links are always admin
	UserID    string // reset
	CreatedBy string // the admin creating it, if any
	TTL       time.Duration
}

// Redeem is what the person redeeming a link sends, plus their session's
// details. Email and Name are only read for setup (both) and invite (Name).
type Redeem struct {
	Email        string
	Name         string
	PasswordHash string
	IP           string
	UserAgent    string
	SessionTTL   time.Duration
}
