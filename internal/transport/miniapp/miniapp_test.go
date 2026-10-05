package miniapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SMbyM/tutoring-bot/internal/domain"
	"github.com/SMbyM/tutoring-bot/internal/store"
)

const token = "123:TEST"

// sign собирает initData так же, как это делает Telegram.
func sign(tgID int64, authDate time.Time) string {
	v := url.Values{}
	v.Set("auth_date", strconv.FormatInt(authDate.Unix(), 10))
	v.Set("query_id", "AAH")
	v.Set("user", `{"id":`+strconv.FormatInt(tgID, 10)+`,"first_name":"Анна"}`)
	keys := []string{}
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := []string{}
	for _, k := range keys {
		lines = append(lines, k+"="+v.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	v.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return v.Encode()
}

func TestValidateInitData(t *testing.T) {
	now := time.Now()
	id, err := ValidateInitData(sign(42, now), token, now, time.Hour)
	if err != nil || id != 42 {
		t.Fatalf("валидные данные: %d, %v", id, err)
	}
	if _, err := ValidateInitData(sign(42, now), "999:OTHER", now, time.Hour); err == nil {
		t.Error("чужой токен должен отклоняться")
	}
	tampered := strings.Replace(sign(42, now), "42", "43", 1)
	if _, err := ValidateInitData(tampered, token, now, time.Hour); err == nil {
		t.Error("подменённый пользователь должен отклоняться")
	}
	if _, err := ValidateInitData(sign(42, now.Add(-2*time.Hour)), token, now, time.Hour); err == nil {
		t.Error("устаревшие данные должны отклоняться")
	}
}

func TestScheduleAPI(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ResetForTests(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, store.Identity{Provider: "tg", ExternalID: "42", ChatID: "42"}, "Анна", domain.RoleTutor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, store.Identity{Provider: "tg", ExternalID: "77", ChatID: "77"}, "Петя", domain.RoleStudent); err != nil {
		t.Fatal(err)
	}
	h := (&Server{S: s, BotToken: token}).Handler()
	call := func(tgID int64, method, path, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "tma "+sign(tgID, time.Now()))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	if code, _ := call(77, "GET", "/api/schedule", ""); code != http.StatusForbidden {
		t.Errorf("ученик не должен видеть редактор: %d", code)
	}
	code, out := call(42, "PUT", "/api/windows", `{"windows":[{"weekday":1,"start":"15:00","end":"19:00"},{"weekday":3,"start":"10:00","end":"12:30"}]}`)
	if code != 200 || len(out["windows"].([]any)) != 2 {
		t.Fatalf("сохранение окон: %d %v", code, out)
	}
	if code, out := call(42, "PUT", "/api/windows", `{"windows":[{"weekday":1,"start":"15:00","end":"19:00"},{"weekday":1,"start":"18:00","end":"20:00"}]}`); code != 400 {
		t.Errorf("пересекающиеся окна должны отклоняться: %d %v", code, out)
	}
	if code, _ := call(42, "PUT", "/api/duration", `{"minutes":90}`); code != 200 {
		t.Error("длительность")
	}
	code, out = call(42, "POST", "/api/exceptions", `{"from":"2030-01-02","to":"2030-01-05","note":"отпуск"}`)
	if code != 200 || len(out["exceptions"].([]any)) != 1 {
		t.Fatalf("исключение: %d %v", code, out)
	}
	exID := int64(out["exceptions"].([]any)[0].(map[string]any)["id"].(float64))
	code, out = call(42, "DELETE", "/api/exceptions/"+strconv.FormatInt(exID, 10), "")
	if code != 200 || len(out["exceptions"].([]any)) != 0 {
		t.Errorf("удаление исключения: %d %v", code, out)
	}
	code, out = call(42, "GET", "/api/schedule", "")
	if code != 200 || out["lesson_minutes"].(float64) != 90 {
		t.Errorf("итог: %v", out)
	}

	// статика отдаётся без авторизации
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/app/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Окна для записи") {
		t.Errorf("страница Mini App: %d", rec.Code)
	}
}
