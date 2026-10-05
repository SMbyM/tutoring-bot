// Package miniapp — HTTP-сервер Telegram Mini App: редактор окон расписания для репетитора.
// Авторизация — подпись initData от Telegram, без отдельных логинов и паролей.
package miniapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SMbyM/tutoring-bot/core"
	"github.com/SMbyM/tutoring-bot/domain"
)

//go:embed static
var staticFS embed.FS

// Server — тонкий HTTP-адаптер поверх core: права и проверки данных делает core.
type Server struct {
	App      *core.App
	BotToken string
	Log      *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /app/", http.StripPrefix("/app/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/schedule", s.auth(s.getSchedule))
	mux.HandleFunc("PUT /api/windows", s.auth(s.putWindows))
	mux.HandleFunc("PUT /api/duration", s.auth(s.putDuration))
	mux.HandleFunc("POST /api/exceptions", s.auth(s.addException))
	mux.HandleFunc("DELETE /api/exceptions/{id}", s.auth(s.deleteException))
	return mux
}

// ValidateInitData проверяет подпись Telegram WebApp initData и возвращает id пользователя Telegram.
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func ValidateInitData(initData, botToken string, now time.Time, maxAge time.Duration) (int64, error) {
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return 0, err
	}
	hash := vals.Get("hash")
	if hash == "" {
		return 0, errors.New("нет подписи")
	}
	vals.Del("hash")
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + vals.Get(k)
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(hash)) {
		return 0, errors.New("неверная подпись")
	}
	authDate, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil || now.Sub(time.Unix(authDate, 0)) > maxAge {
		return 0, errors.New("данные устарели")
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(vals.Get("user")), &user); err != nil || user.ID == 0 {
		return 0, errors.New("нет пользователя")
	}
	return user.ID, nil
}

// auth определяет пользователя по подписанному initData. Кто имеет право редактировать
// расписание, решает core (сейчас — только репетитор).
func (s *Server) auth(next func(http.ResponseWriter, *http.Request, domain.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := strings.TrimPrefix(r.Header.Get("Authorization"), "tma ")
		tgID, err := ValidateInitData(initData, s.BotToken, time.Now(), 24*time.Hour)
		if err != nil {
			httpErr(w, http.StatusUnauthorized, "Откройте редактор из бота: "+err.Error())
			return
		}
		u, ok, err := s.App.FindUser(r.Context(), "tg", strconv.FormatInt(tgID, 10))
		if err != nil {
			s.fail(w, err)
			return
		}
		if !ok {
			httpErr(w, http.StatusForbidden, "Сначала зарегистрируйтесь в боте")
			return
		}
		next(w, r, u)
	}
}

// fail переводит ошибки core в HTTP-ответы.
func (s *Server) fail(w http.ResponseWriter, err error) {
	var ie domain.InputError
	switch {
	case errors.Is(err, domain.ErrNotAllowed):
		httpErr(w, http.StatusForbidden, "Редактор расписания доступен только репетиторам")
	case errors.As(err, &ie):
		httpErr(w, http.StatusBadRequest, ie.Error())
	case errors.Is(err, domain.ErrNotFound):
		httpErr(w, http.StatusNotFound, "не найдено")
	default:
		if s.Log != nil {
			s.Log.Error("miniapp", "err", err)
		}
		httpErr(w, http.StatusInternalServerError, "внутренняя ошибка")
	}
}

type windowDTO struct {
	Weekday int    `json:"weekday"` // 0=вс … 6=сб
	Start   string `json:"start"`   // «15:00»
	End     string `json:"end"`
}

type exceptionDTO struct {
	ID   int64  `json:"id,omitempty"`
	From string `json:"from"` // YYYY-MM-DD
	To   string `json:"to"`
	Note string `json:"note"`
}

func hm(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

func parseHM(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		if s == "24:00" {
			return 24 * 60, nil
		}
		return 0, errors.New("время " + s + " некорректно")
	}
	return t.Hour()*60 + t.Minute(), nil
}

func (s *Server) getSchedule(w http.ResponseWriter, r *http.Request, u domain.User) {
	sc, err := s.App.MySchedule(r.Context(), u)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := struct {
		Name          string         `json:"name"`
		LessonMinutes int            `json:"lesson_minutes"`
		Windows       []windowDTO    `json:"windows"`
		Exceptions    []exceptionDTO `json:"exceptions"`
	}{Name: sc.Tutor.Name, LessonMinutes: sc.Tutor.LessonMinutes, Windows: []windowDTO{}, Exceptions: []exceptionDTO{}}
	for _, w := range sc.Windows {
		out.Windows = append(out.Windows, windowDTO{int(w.Weekday), hm(w.StartMin), hm(w.EndMin)})
	}
	for _, e := range sc.Exceptions {
		out.Exceptions = append(out.Exceptions, exceptionDTO{e.ID, e.From.Format("2006-01-02"), e.To.Format("2006-01-02"), e.Note})
	}
	writeJSON(w, out)
}

func (s *Server) putWindows(w http.ResponseWriter, r *http.Request, u domain.User) {
	var in struct {
		Windows []windowDTO `json:"windows"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		httpErr(w, 400, "неверный запрос")
		return
	}
	ws := make([]domain.Window, 0, len(in.Windows))
	for _, d := range in.Windows {
		st, err1 := parseHM(d.Start)
		en, err2 := parseHM(d.End)
		if err := errors.Join(err1, err2); err != nil || d.Weekday < 0 || d.Weekday > 6 {
			httpErr(w, 400, "проверьте время окон")
			return
		}
		ws = append(ws, domain.Window{Weekday: time.Weekday(d.Weekday), StartMin: st, EndMin: en})
	}
	if err := s.App.SetWindows(r.Context(), u, ws); err != nil {
		s.fail(w, err)
		return
	}
	s.getSchedule(w, r, u)
}

func (s *Server) putDuration(w http.ResponseWriter, r *http.Request, u domain.User) {
	var in struct {
		Minutes int `json:"minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, 400, "неверный запрос")
		return
	}
	if err := s.App.SetLessonMinutes(r.Context(), u, in.Minutes); err != nil {
		s.fail(w, err)
		return
	}
	s.getSchedule(w, r, u)
}

func (s *Server) addException(w http.ResponseWriter, r *http.Request, u domain.User) {
	var in exceptionDTO
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, 400, "неверный запрос")
		return
	}
	loc := u.Location()
	from, err1 := time.ParseInLocation("2006-01-02", in.From, loc)
	to, err2 := time.ParseInLocation("2006-01-02", in.To, loc)
	if errors.Join(err1, err2) != nil {
		httpErr(w, 400, "проверьте даты")
		return
	}
	if err := s.App.AddException(r.Context(), u, from, to, in.Note); err != nil {
		s.fail(w, err)
		return
	}
	s.getSchedule(w, r, u)
}

func (s *Server) deleteException(w http.ResponseWriter, r *http.Request, u domain.User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, 400, "неверный id")
		return
	}
	if err := s.App.DeleteException(r.Context(), u, id); err != nil {
		s.fail(w, err)
		return
	}
	s.getSchedule(w, r, u)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, text string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": text})
}
