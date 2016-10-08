package main

import (
	"bytes"
	"errors"
	"github.com/robarchibald/substring"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

var futureTime time.Time = time.Now().Add(5 * time.Minute)
var pastTime time.Time = time.Now().Add(-5 * time.Minute)

func getAuthStore(createSessionReturn *SessionReturn, emailCookieToReturn *EmailCookie, hasCookieGetError, hasCookiePutError bool, backend *MockBackend) *AuthStore {
	r := &http.Request{}
	cookieStore := NewMockCookieStore(map[string]interface{}{emailCookieName: emailCookieToReturn}, hasCookieGetError, hasCookiePutError)
	sessionStore := MockSessionStore{CreateSessionReturn: createSessionReturn}
	return &AuthStore{backend, &sessionStore, &TextMailer{}, cookieStore, r}
}

func TestNewAuthStore(t *testing.T) {
	w := httptest.NewRecorder()
	r := &http.Request{}
	b := &MockBackend{}
	m := &TextMailer{}
	actual := NewAuthStore(b, m, w, r, cookieKey, "prefix", false)
	if actual.backend != b || actual.cookieStore.(*CookieStore).w != w || actual.cookieStore.(*CookieStore).r != r {
		t.Fatal("expected correct init")
	}
}

func TestAuthStoreEndToEnd(t *testing.T) {
	w := httptest.NewRecorder()
	r := &http.Request{Header: http.Header{}}
	b := NewBackendMemory()
	m := &TextMailer{}
	s := NewAuthStore(b, m, w, r, cookieKey, "prefix", false)

	// register new user
	// adds to users, logins and sessions
	err := s.register("test@test.com")
	if err != nil || len(b.Users) != 1 || b.Users[0].EmailVerified || len(b.Sessions) != 0 {
		t.Fatal("expected to be able to add user")
	}

	// get code from "email"
	data := m.MessageData.(*SendVerifyParams)

	// verify email
	err = s.verifyEmail(data.VerificationCode)

	// decode email cookie
	value := substring.Between(w.HeaderMap["Set-Cookie"][0], "prefixEmail=", ";")
	emailCookie := EmailCookie{}
	cookieStore.Decode("prefixEmail", value, &emailCookie)
	emailVerifyHash, _ := decodeStringToHash(emailCookie.EmailVerificationCode)
	if err != nil || len(b.Users) != 1 || !b.Users[0].EmailVerified || emailVerifyHash != b.Users[0].EmailVerifyHash {
		t.Fatal("expected email to be verified", err, data.Email, b.Users)
	}

	// add email cookie to the next request
	r.AddCookie(newCookie("prefixEmail", value, false, emailExpireMins))

	// create profile
	err = s.createProfile("fullName", "company", "password", "picturePath")

	// decode session cookie
	value = substring.Between(w.HeaderMap["Set-Cookie"][1], "prefixSession=", ";")
	sessionCookie := SessionCookie{}
	cookieStore.Decode("prefixSession", value, &sessionCookie)
	sessionHash, _ := decodeStringToHash(sessionCookie.SessionId)

	// add session cookie to the next request
	r.AddCookie(newCookie("prefixSession", value, false, emailExpireMins))

	if err != nil || len(b.Sessions) != 1 || b.Sessions[0].SessionHash != sessionHash || len(b.Logins) != 1 || b.Logins[0].UserId != 1 ||
		b.Users[0].FullName != "fullName" || b.Users[0].PrimaryEmail != "test@test.com" {
		t.Fatal("expected profile to be created", err, b.Sessions[0].SessionHash, b.Logins[0].UserId, b.Users[0].FullName, b.Users[0].PrimaryEmail)
	}

	// login on same browser with same existing session
	session, err := s.login("test@test.com", "password", true)
	if err != nil || len(b.Logins) != 1 || len(b.Sessions) != 1 || len(b.Users) != 1 || session.SessionHash != b.Sessions[0].SessionHash || session.UserId != 1 {
		t.Fatal("expected to login to existing session", len(b.Logins), len(b.Sessions), len(b.Users), session, b.Sessions[0].SessionHash)
	}

	// now login with different browser with new session ID. Create new session
	/*	session, rememberMe, err = b.NewLoginSession(login.LoginId, "newSessionHash", time.Now().UTC().AddDate(0, 0, 1), time.Now().UTC().AddDate(0, 0, 5), false, "", "", time.Time{}, time.Time{})
		if err != nil || login == nil || rememberMe != nil || len(b.Sessions) != 2 {
			t.Fatal("expected new User Login to be created")
		}*/
}

var loginTests = []struct {
	Scenario            string
	Email               string
	Password            string
	RememberMe          bool
	CreateSessionReturn *SessionReturn
	GetUserLoginReturn  *LoginReturn
	ErrReturn           error
	MethodsCalled       []string
	ExpectedResult      *UserLoginRememberMe
	ExpectedErr         string
}{
	{
		Scenario:    "Invalid email",
		Email:       "invalid@bogus",
		ExpectedErr: "Please enter a valid email address.",
	},
	{
		Scenario:    "Invalid password",
		Email:       "email@example.com",
		Password:    "short",
		ExpectedErr: passwordValidationMessage,
	},
	{
		Scenario:           "Can't get login",
		Email:              "email@example.com",
		Password:           "validPassword",
		GetUserLoginReturn: loginErr(),
		MethodsCalled:      []string{"GetUserLogin"},
		ExpectedErr:        "Invalid username or password",
	},
	{
		Scenario:           "Incorrect password",
		Email:              "email@example.com",
		Password:           "wrongPassword",
		GetUserLoginReturn: &LoginReturn{Login: &UserLogin{LoginId: 1, UserId: 1, ProviderKey: "1234"}},
		MethodsCalled:      []string{"GetUserLogin"},
		ExpectedErr:        "Invalid username or password",
	},
	{
		Scenario:            "Got session",
		Email:               "email@example.com",
		Password:            "correctPassword",
		GetUserLoginReturn:  loginSuccess(),
		CreateSessionReturn: sessionSuccess(futureTime, futureTime),
		MethodsCalled:       []string{"GetUserLogin"},
	},
}

func TestAuthLogin(t *testing.T) {
	for i, test := range loginTests {
		backend := &MockBackend{GetUserLoginReturn: test.GetUserLoginReturn, ErrReturn: test.ErrReturn}
		store := getAuthStore(test.CreateSessionReturn, nil, true, false, backend) // cookie get error so we don't try to invalidate old session or rememberme
		val, err := store.login(test.Email, test.Password, test.RememberMe)
		methods := store.backend.(*MockBackend).MethodsCalled
		if (err == nil && test.ExpectedErr != "" || err != nil && test.ExpectedErr != err.Error()) ||
			!collectionEqual(test.MethodsCalled, methods) {
			t.Errorf("Scenario[%d] failed: %s\nexpected err:%v\tactual err:%v\nexpected val:%v\tactual val:%v\nexpected methods: %s\tactual methods: %s", i, test.Scenario, test.ExpectedErr, err, test.ExpectedResult, val, test.MethodsCalled, methods)
		}
	}
}

var registerTests = []struct {
	Scenario      string
	Email         string
	AddUserReturn error
	MethodsCalled []string
	ExpectedErr   string
}{
	{
		Scenario:    "Invalid email",
		Email:       "invalid@bogus",
		ExpectedErr: "Invalid email",
	},
	{
		Scenario:      "Add User error",
		Email:         "validemail@test.com",
		AddUserReturn: errors.New("failed"),
		MethodsCalled: []string{"AddUser"},
		ExpectedErr:   "Unable to save user",
	},
	{
		Scenario:      "Send verify email",
		Email:         "validemail@test.com",
		MethodsCalled: []string{"AddUser"},
	},
}

func TestAuthRegister(t *testing.T) {
	for i, test := range registerTests {
		backend := &MockBackend{AddUserReturn: test.AddUserReturn}
		store := getAuthStore(nil, nil, false, false, backend)
		err := store.register(test.Email)
		methods := store.backend.(*MockBackend).MethodsCalled
		if (err == nil && test.ExpectedErr != "" || err != nil && test.ExpectedErr != err.Error()) ||
			!collectionEqual(test.MethodsCalled, methods) {
			t.Errorf("Scenario[%d] failed: %s\nexpected err:%v\tactual err:%v\nexpected methods: %s\tactual methods: %s", i, test.Scenario, test.ExpectedErr, err, test.MethodsCalled, methods)
		}
	}
}

var createProfileTests = []struct {
	Scenario            string
	HasCookieGetError   bool
	HasCookiePutError   bool
	EmailCookie         *EmailCookie
	CreateLoginReturn   *LoginReturn
	CreateSessionReturn *SessionReturn
	MethodsCalled       []string
	ExpectedErr         string
}{
	{
		Scenario:          "Error Getting email cookie",
		HasCookieGetError: true,
		ExpectedErr:       "Unable to get email verification cookie",
	},
	{
		Scenario:    "Invalid verification code",
		EmailCookie: &EmailCookie{EmailVerificationCode: "12345", ExpireTimeUTC: time.Now()},
		ExpectedErr: "Invalid email verification cookie",
	},
	{
		Scenario:          "Error Creating profile",
		EmailCookie:       &EmailCookie{EmailVerificationCode: "nfwRDzfxxJj2_HY-_mLz6jWyWU7bF0zUlIUUVkQgbZ0=", ExpireTimeUTC: time.Now()},
		CreateLoginReturn: loginErr(),
		MethodsCalled:     []string{"CreateLogin"},
		ExpectedErr:       "Unable to create profile",
	},
	{
		Scenario:            "Error getting session",
		EmailCookie:         &EmailCookie{EmailVerificationCode: "nfwRDzfxxJj2_HY-_mLz6jWyWU7bF0zUlIUUVkQgbZ0=", ExpireTimeUTC: time.Now()},
		HasCookiePutError:   true,
		CreateLoginReturn:   loginSuccess(),
		CreateSessionReturn: sessionErr(),
		MethodsCalled:       []string{"CreateLogin"},
		ExpectedErr:         "failed",
	},
	{
		Scenario:            "Success",
		EmailCookie:         &EmailCookie{EmailVerificationCode: "nfwRDzfxxJj2_HY-_mLz6jWyWU7bF0zUlIUUVkQgbZ0=", ExpireTimeUTC: time.Now()},
		CreateLoginReturn:   loginSuccess(),
		CreateSessionReturn: sessionSuccess(futureTime, futureTime),
		MethodsCalled:       []string{"CreateLogin"},
	},
}

func TestAuthCreateProfile(t *testing.T) {
	for i, test := range createProfileTests {
		backend := &MockBackend{CreateLoginReturn: test.CreateLoginReturn}
		store := getAuthStore(test.CreateSessionReturn, test.EmailCookie, test.HasCookieGetError, test.HasCookiePutError, backend)
		err := store.createProfile("name", "organization", "password", "path")
		methods := store.backend.(*MockBackend).MethodsCalled
		if (err == nil && test.ExpectedErr != "" || err != nil && test.ExpectedErr != err.Error()) ||
			!collectionEqual(test.MethodsCalled, methods) {
			t.Errorf("Scenario[%d] failed: %s\nexpected err:%v\tactual err:%v\nexpected methods: %s\tactual methods: %s", i, test.Scenario, test.ExpectedErr, err, test.MethodsCalled, methods)
		}
	}
}

var verifyEmailTests = []struct {
	Scenario              string
	EmailVerificationCode string
	VerifyEmailReturn     *VerifyEmailReturn
	MethodsCalled         []string
	ExpectedErr           string
}{
	{
		Scenario:              "Decode error",
		EmailVerificationCode: "code",
		VerifyEmailReturn:     verifyEmailErr(),
		ExpectedErr:           "Invalid verification code",
	},
	{
		Scenario:              "Verify Email Error",
		EmailVerificationCode: "nfwRDzfxxJj2_HY-_mLz6jWyWU7bF0zUlIUUVkQgbZ0",
		VerifyEmailReturn:     verifyEmailErr(),
		MethodsCalled:         []string{"VerifyEmail"},
		ExpectedErr:           "Failed to verify email",
	},
	{
		Scenario:              "Email sent",
		EmailVerificationCode: "nfwRDzfxxJj2_HY-_mLz6jWyWU7bF0zUlIUUVkQgbZ0",
		VerifyEmailReturn:     verifyEmailSuccess(),
		MethodsCalled:         []string{"VerifyEmail"},
	},
}

func TestAuthVerifyEmail(t *testing.T) {
	for i, test := range verifyEmailTests {
		backend := &MockBackend{VerifyEmailReturn: test.VerifyEmailReturn}
		store := getAuthStore(nil, nil, false, false, backend)
		err := store.verifyEmail(test.EmailVerificationCode)
		methods := store.backend.(*MockBackend).MethodsCalled
		if (err == nil && test.ExpectedErr != "" || err != nil && test.ExpectedErr != err.Error()) ||
			!collectionEqual(test.MethodsCalled, methods) {
			t.Errorf("Scenario[%d] failed: %s\nexpected err:%v\tactual err:%v\nexpected methods: %s\tactual methods: %s", i, test.Scenario, test.ExpectedErr, err, test.MethodsCalled, methods)
		}
	}
}

func TestGetRegistration(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"Email":"my.email@test.com"}`)
	r := &http.Request{Body: ioutil.NopCloser(&buf)}
	reg, _ := getRegistration(r)
	if reg.Email != "my.email@test.com" {
		t.Error("expected registration to be filled", reg)
	}
}

func TestRegisterPub(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"Email":"bogus"}`)
	r := &http.Request{Body: ioutil.NopCloser(&buf)}
	backend := &MockBackend{}
	store := getAuthStore(nil, nil, true, false, backend)
	store.r = r
	err := store.Register()
	if err == nil || err.Error() != "Invalid email" {
		t.Error("expected error from child register method", err)
	}

	buf.WriteString("b")
	err = store.Register()
	if err == nil || !strings.HasPrefix(err.Error(), "Unable to get email") {
		t.Error("expected error from parent Register method", err)
	}
}

func TestGetVerificationCode(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"EmailVerificationCode":"code"}`)
	r := &http.Request{Body: ioutil.NopCloser(&buf)}
	verify, _ := getVerificationCode(r)
	if verify.EmailVerificationCode != "code" {
		t.Error("expected verify to be filled", verify)
	}
}

func TestVerifyEmailPub(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"EmailVerificationCode":"nfwRDzfxxJj2_HY-_mLz6jWyWU7bF0zUlIUUVkQgbZ0"}`) // random valid base64 encoded data
	r := &http.Request{Body: ioutil.NopCloser(&buf)}
	backend := &MockBackend{VerifyEmailReturn: verifyEmailErr()}
	store := getAuthStore(nil, nil, true, false, backend)
	store.r = r
	err := store.VerifyEmail()
	if err == nil || err.Error() != "Failed to verify email" {
		t.Error("expected error from child verifyEmail method", err)
	}

	buf.WriteString("b")
	err = store.VerifyEmail()
	if err == nil || err.Error() != "Unable to get verification email from JSON" {
		t.Error("expected error from VerifyEmail method", err)
	}
}

func TestGetCredentials(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"Email":"email", "Password":"password", "RememberMe":true}`)
	r := &http.Request{Body: ioutil.NopCloser(&buf)}
	credentials, err := getCredentials(r)
	if err != nil || credentials.Email != "email" || credentials.Password != "password" || credentials.RememberMe != true {
		t.Error("expected credentials to be filled", credentials, err)
	}
}

func TestLoginJson(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"Email":"email", "Password":"password", "RememberMe":true}`)
	r := &http.Request{Body: ioutil.NopCloser(&buf)}
	backend := &MockBackend{}
	store := getAuthStore(nil, nil, true, false, backend)
	store.r = r
	err := store.Login()
	if err == nil || err.Error() != "Please enter a valid email address." {
		t.Error("expected error from login method", err)
	}

	buf.WriteString("b")
	err = store.Login()
	if err == nil || err.Error() != "Unable to get credentials" {
		t.Error("expected error from login method", err)
	}

}

func TestGetProfile(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("fullName", "name")
	w.WriteField("Organization", "org")
	w.WriteField("password", "pass")
	file, _ := os.Open("cover.out")
	data, _ := ioutil.ReadAll(file)
	tmpFile, _ := ioutil.TempFile("", "profile")
	part, _ := w.CreateFormFile("file", tmpFile.Name())
	part.Write(data)
	w.Close()

	r, _ := http.NewRequest("PUT", "url", &buf)
	r.Header.Add("Content-Type", w.FormDataContentType())
	profile, _ := getProfile(r)
	if profile.FullName != "name" || profile.Organization != "org" || profile.Password != "pass" {
		t.Error("expected correct profile", profile)
	}
}

func TestCreateProfilePub(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	file, _ := os.Open("cover.out")
	data, _ := ioutil.ReadAll(file)
	tmpFile, _ := ioutil.TempFile("", "profile")
	part, _ := w.CreateFormFile("file", tmpFile.Name())
	part.Write(data)
	w.Close()

	r, _ := http.NewRequest("PUT", "url", &buf)
	r.Header.Add("Content-Type", w.FormDataContentType())
	backend := &MockBackend{}
	store := getAuthStore(nil, nil, true, false, backend)
	store.r = r
	err := store.CreateProfile()
	if err == nil || err.Error() != "Unable to get email verification cookie" {
		t.Error("expected error from CreateProfile method", err)
	}

	store.r = &http.Request{Body: ioutil.NopCloser(&buf)}
	err = store.CreateProfile()
	if err == nil || err.Error() != "Unable to get profile information from form" {
		t.Error("expected error from CreateProfile method", err)
	}
}

func TestGetBaseUrl(t *testing.T) {
	actual := getBaseUrl("http://www.hello.com/anywhere/but/here.html")
	if actual != "http://www.hello.com" {
		t.Error("expected base url", actual)
	}

	actual = getBaseUrl("http://www.hello.com")
	if actual != "http://www.hello.com" {
		t.Error("expected base url", actual)
	}

	actual = getBaseUrl("anywhere/but/here.html")
	if actual != "https://endfirst.com" {
		t.Error("expected base url", actual)
	}
}

func collectionEqual(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	for i, val := range expected {
		if actual[i] != val {
			return false
		}
	}
	return true
}

/****************************************************************************/
type MockAuthStore struct {
}

func NewMockAuthStore() *MockAuthStore {
	return &MockAuthStore{}
}

func (s *MockAuthStore) Get() (*UserLoginSession, error) {
	return nil, nil
}
func (s *MockAuthStore) GetRememberMe() (*UserLoginRememberMe, error) {
	return nil, nil
}
func (s *MockAuthStore) Login(email, password, returnUrl string) (*UserLoginSession, error) {
	return nil, nil
}