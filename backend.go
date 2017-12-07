package auth

import (
	"time"

	"github.com/pkg/errors"
)

var errEmailVerifyHashExists = errors.New("DB: Email verify hash already exists")
var errInvalidEmailVerifyHash = errors.New("DB: Invalid verify code")
var errInvalidRenewTimeUTC = errors.New("DB: Invalid RenewTimeUTC")
var errInvalidSessionHash = errors.New("DB: Invalid SessionHash")
var errRememberMeSelectorExists = errors.New("DB: RememberMe selector already exists")
var errUserNotFound = errors.New("DB: User not found")
var errLoginNotFound = errors.New("DB: Login not found")
var errInvalidCredentials = errors.New("DB: Invalid Credentials")
var errSessionNotFound = errors.New("DB: Session not found")
var errSessionAlreadyExists = errors.New("DB: Session already exists")
var errRememberMeNotFound = errors.New("DB: RememberMe not found")
var errRememberMeNeedsRenew = errors.New("DB: RememberMe needs to be renewed")
var errRememberMeExpired = errors.New("DB: RememberMe is expired")
var errUserAlreadyExists = errors.New("DB: User already exists")

// Backender interface contains all the methods needed to read and write users, sessions and logins
type Backender interface {
	// UserBackender. Write out since it contains duplicate BackendCloser
	AddUser(email string) (int, error)
	GetUser(email string) (*user, error)
	UpdateUser(userID int, fullname string, company string, pictureURL string) error

	// LoginBackender. Write out since it contains duplicate BackendCloser
	CreateLogin(userID int, email, password, fullName string) (*UserLogin, error)
	GetLogin(email string) (*UserLogin, error)
	Login(email, password string) (*UserLogin, error)
	UpdateEmail(email string, password string, newEmail string) (*LoginSession, error)
	UpdatePassword(email string, oldPassword string, newPassword string) (*LoginSession, error)

	sessionBackender
}

type backendCloser interface {
	Close() error
}

type userBackender interface {
	AddUser(email string) (int, error)
	GetUser(email string) (*user, error)
	UpdateUser(userID int, fullname string, company string, pictureURL string) error
	backendCloser
}

type loginBackender interface {
	CreateLogin(userID int, email, password, fullName string) (*UserLogin, error)
	GetLogin(email string) (*UserLogin, error)
	Login(email, password string) (*UserLogin, error)
	UpdateEmail(email string, password string, newEmail string) (*LoginSession, error)
	UpdatePassword(email string, oldPassword string, newPassword string) (*LoginSession, error)
	backendCloser
}

type sessionBackender interface {
	CreateEmailSession(email, emailVerifyHash, destinationURL string) error
	GetEmailSession(verifyHash string) (*emailSession, error)
	UpdateEmailSession(verifyHash string, userID int, email, destinationURL string) error
	DeleteEmailSession(verifyHash string) error

	CreateSession(userID int, email, fullname, sessionHash string, sessionRenewTimeUTC, sessionExpireTimeUTC time.Time, rememberMe bool, rememberMeSelector, rememberMeTokenHash string, rememberMeRenewTimeUTC, rememberMeExpireTimeUTC time.Time) (*LoginSession, *rememberMeSession, error)
	GetSession(sessionHash string) (*LoginSession, error)
	RenewSession(sessionHash string, renewTimeUTC time.Time) (*LoginSession, error)
	InvalidateSession(sessionHash string) error
	InvalidateSessions(email string) error

	GetRememberMe(selector string) (*rememberMeSession, error)
	RenewRememberMe(selector string, renewTimeUTC time.Time) (*rememberMeSession, error)
	InvalidateRememberMe(selector string) error
	backendCloser
}

type emailSession struct {
	UserID          int
	Email           string
	EmailVerifyHash string
	DestinationURL  string
}

type user struct {
	UserID            int
	FullName          string
	PrimaryEmail      string
	LockoutEndTimeUTC *time.Time
	AccessFailedCount int
}

type UserLogin struct {
	UserID   int
	Email    string
	FullName string
}

type LoginSession struct {
	UserID        int
	Email         string
	FullName      string
	SessionHash   string
	RenewTimeUTC  time.Time
	ExpireTimeUTC time.Time
}

type rememberMeSession struct {
	UserID        int
	Email         string
	Selector      string
	TokenHash     string
	RenewTimeUTC  time.Time
	ExpireTimeUTC time.Time
}

type loginProvider struct {
	LoginProviderID   int
	Name              string
	OAuthClientID     string
	OAuthClientSecret string
	OAuthURL          string
}

type AuthError struct {
	message    string
	innerError error
	shouldLog  bool
	error
}

func newLoggedError(message string, innerError error) *AuthError {
	return &AuthError{message: message, innerError: innerError, shouldLog: true}
}

func newAuthError(message string, innerError error) *AuthError {
	return &AuthError{message: message, innerError: innerError}
}

func (a *AuthError) Error() string {
	return a.message
}

func (a *AuthError) Trace() string {
	trace := a.message + "\n"
	indent := "  "
	inner := a.innerError
	for inner != nil {
		trace += indent + inner.Error() + "\n"
		e, ok := inner.(*AuthError)
		if !ok {
			break
		}
		indent += "  "
		inner = e.innerError
	}
	return trace
}

type backend struct {
	u userBackender
	l loginBackender
	s sessionBackender
	backendCloser
}

// NewBackend returns a Backender from a UserBackender, LoginBackender and SessionBackender
func NewBackend(u userBackender, l loginBackender, s sessionBackender) Backender {
	return &backend{u: u, l: l, s: s}
}

func (b *backend) GetLogin(email string) (*UserLogin, error) {
	return b.l.GetLogin(email)
}

func (b *backend) Login(email, password string) (*UserLogin, error) {
	return b.l.Login(email, password)
}

func (b *backend) CreateSession(userID int, email, fullname, sessionHash string, sessionRenewTimeUTC, sessionExpireTimeUTC time.Time, rememberMe bool, rememberMeSelector, rememberMeTokenHash string, rememberMeRenewTimeUTC, rememberMeExpireTimeUTC time.Time) (*LoginSession, *rememberMeSession, error) {
	return b.s.CreateSession(userID, email, fullname, sessionHash, sessionRenewTimeUTC, sessionExpireTimeUTC, rememberMe, rememberMeSelector, rememberMeTokenHash, rememberMeRenewTimeUTC, rememberMeExpireTimeUTC)
}

func (b *backend) GetSession(sessionHash string) (*LoginSession, error) {
	return b.s.GetSession(sessionHash)
}

func (b *backend) RenewSession(sessionHash string, renewTimeUTC time.Time) (*LoginSession, error) {
	return b.s.RenewSession(sessionHash, renewTimeUTC)
}

func (b *backend) GetRememberMe(selector string) (*rememberMeSession, error) {
	return b.s.GetRememberMe(selector)
}

func (b *backend) RenewRememberMe(selector string, renewTimeUTC time.Time) (*rememberMeSession, error) {
	return b.s.RenewRememberMe(selector, renewTimeUTC)
}

func (b *backend) CreateEmailSession(email, emailVerifyHash, destinationURL string) error {
	return b.s.CreateEmailSession(email, emailVerifyHash, destinationURL)
}

func (b *backend) GetEmailSession(emailVerifyHash string) (*emailSession, error) {
	return b.s.GetEmailSession(emailVerifyHash)
}

func (b *backend) UpdateEmailSession(emailVerifyHash string, userID int, email, destinationURL string) error {
	return b.s.UpdateEmailSession(emailVerifyHash, userID, email, destinationURL)
}

func (b *backend) DeleteEmailSession(emailVerifyHash string) error {
	return b.s.DeleteEmailSession(emailVerifyHash)
}

func (b *backend) AddUser(email string) (int, error) {
	return b.u.AddUser(email)
}

func (b *backend) GetUser(email string) (*user, error) {
	return b.u.GetUser(email)
}

func (b *backend) UpdateUser(userID int, fullname string, company string, pictureURL string) error {
	return b.u.UpdateUser(userID, fullname, company, pictureURL)
}

func (b *backend) CreateLogin(userID int, email, password, fullName string) (*UserLogin, error) {
	return b.l.CreateLogin(userID, email, password, fullName)
}

func (b *backend) UpdateEmail(email string, password string, newEmail string) (*LoginSession, error) {
	return b.l.UpdateEmail(email, password, newEmail)
}

func (b *backend) UpdatePassword(email string, oldPassword string, newPassword string) (*LoginSession, error) {
	return b.l.UpdatePassword(email, oldPassword, newPassword)
}

func (b *backend) InvalidateSession(sessionHash string) error {
	return b.s.InvalidateSession(sessionHash)
}

func (b *backend) InvalidateSessions(email string) error {
	return b.s.InvalidateSessions(email)
}

func (b *backend) InvalidateRememberMe(selector string) error {
	return b.s.InvalidateRememberMe(selector)
}

func (b *backend) Close() error {
	if err := b.s.Close(); err != nil {
		return err
	}
	if err := b.u.Close(); err != nil {
		return err
	}
	if err := b.l.Close(); err != nil {
		return err
	}
	return nil
}
