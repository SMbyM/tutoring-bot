package actions

import (
	"fmt"
	"time"
)

// ---- значения-перечисления (проверяются при Decode) ----

// BookMode — режим записи.
type BookMode string

const (
	ModeTrial     BookMode = "t" // пробный
	ModeOnce      BookMode = "o" // разовый
	ModeRecurring BookMode = "r" // постоянное время
)

func (m BookMode) valid() bool { return m == ModeTrial || m == ModeOnce || m == ModeRecurring }

// Mark — отметка репетитора после урока.
type Mark string

const (
	MarkHeld   Mark = "held"
	MarkNoShow Mark = "noshow"
	MarkMoved  Mark = "moved"
)

func (m Mark) valid() bool { return m == MarkHeld || m == MarkNoShow || m == MarkMoved }

// Day — дата в формате ГГГГММДД (день в поясе пользователя).
type Day string

func DayOf(t time.Time, loc *time.Location) Day { return Day(t.In(loc).Format("20060102")) }

func (d Day) valid() bool {
	_, err := time.Parse("20060102", string(d))
	return err == nil
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

// ---- регистрация и навигация ----

type Home struct{ Marker }
type Consent struct{ Marker }
type ChooseRole struct {
	Marker
	Role string
}
type KeepProfileName struct{ Marker }
type SetGrade struct {
	Marker
	Grade int
}

// ---- настройки ----

type Settings struct{ Marker }
type ToggleSetting struct {
	Marker
	Key string
}
type SettingsDone struct{ Marker }

// ---- запись ----

type PickSubject struct {
	Marker
	StudentID int64
}
type PickTutor struct {
	Marker
	StudentID int64
	SubjectID int
}
type TutorCard struct {
	Marker
	StudentID int64
	SubjectID int
	TutorID   int64
}

// PickDay — выбор дня для записи.
type PickDay struct {
	Marker
	TutorID   int64
	SubjectID int
	StudentID int64
	Mode      BookMode
}

// PickTime — выбор времени в выбранный день.
type PickTime struct {
	Marker
	TutorID   int64
	SubjectID int
	StudentID int64
	Mode      BookMode
	Day       Day
}

// Book — записаться на выбранное время (unix-секунды).
type Book struct {
	Marker
	TutorID   int64
	SubjectID int
	StudentID int64
	Mode      BookMode
	Start     int64
}

func (a PickDay) validate() error {
	if !a.Mode.valid() {
		return invalid("режим %q", a.Mode)
	}
	return nil
}
func (a PickTime) validate() error {
	if !a.Mode.valid() || !a.Day.valid() {
		return invalid("режим %q или день %q", a.Mode, a.Day)
	}
	return nil
}
func (a Book) validate() error {
	if !a.Mode.valid() {
		return invalid("режим %q", a.Mode)
	}
	return nil
}

func (a Book) StartTime() time.Time { return time.Unix(a.Start, 0) }

type Enroll struct {
	Marker
	TutorID   int64
	SubjectID int
	StudentID int64
}

// ---- оплата ----

type Balances struct {
	Marker
	StudentID int64
}
type PayOptions struct {
	Marker
	StudentID int64
	TutorID   int64
}
type Purchase struct {
	Marker
	StudentID int64
	TutorID   int64
	ProductID int
}

// ---- приглашения и отзывы ----

type InviteParent struct{ Marker }
type Feedback struct {
	Marker
	LessonID int64
	Liked    bool
}
type FeedbackComment struct {
	Marker
	FeedbackID int64
}

// ---- уроки ----

type StudentLessons struct {
	Marker
	StudentID int64
}
type TutorLessons struct{ Marker }
type OpenLesson struct {
	Marker
	LessonID int64
}
type RescheduleDay struct {
	Marker
	LessonID int64
}
type RescheduleTime struct {
	Marker
	LessonID int64
	Day      Day
}
type Reschedule struct {
	Marker
	LessonID int64
	Start    int64
}
type AskCancel struct {
	Marker
	LessonID int64
}
type Cancel struct {
	Marker
	LessonID int64
}
type StopRecurring struct {
	Marker
	SlotID int64
}
type DecideRequest struct {
	Marker
	RequestID int64
	Approve   bool
}
type MarkLesson struct {
	Marker
	LessonID int64
	Mark     Mark
}

func (a RescheduleTime) validate() error {
	if !a.Day.valid() {
		return invalid("день %q", a.Day)
	}
	return nil
}
func (a Reschedule) StartTime() time.Time { return time.Unix(a.Start, 0) }
func (a MarkLesson) validate() error {
	if !a.Mark.valid() {
		return invalid("отметка %q", a.Mark)
	}
	return nil
}

// ---- родитель ----

type Child struct {
	Marker
	KidID int64
}
type ToggleChildApproval struct {
	Marker
	KidID int64
}
type InviteChild struct{ Marker }
type AddChild struct{ Marker }
type ChildGrade struct {
	Marker
	Grade int
}

// ---- репетитор ----

type Windows struct{ Marker }
type EditWindowsText struct{ Marker }
type AskException struct{ Marker }
type DeleteException struct {
	Marker
	ID int64
}
type MyStudents struct{ Marker }
type Profile struct{ Marker }
type EditBio struct{ Marker }
type SetDuration struct {
	Marker
	Minutes int
}
type ToggleSubject struct {
	Marker
	SubjectID int
}

// ---- администратор ----

type AdminTutors struct{ Marker }
type AdminTutor struct {
	Marker
	TutorID int64
}
type AskTutorPrice struct {
	Marker
	TutorID int64
}
type ResetTutorPrice struct {
	Marker
	TutorID int64
}
type InviteTutor struct{ Marker }
type Prices struct{ Marker }
type AskBasePrice struct{ Marker }
type ToggleProduct struct {
	Marker
	ProductID int
}
type AskProduct struct{ Marker }
type FeedbackList struct{ Marker }
type ForwardFeedback struct {
	Marker
	FeedbackID int64
}
type LateCancels struct{ Marker }
type Subjects struct{ Marker }
type AskSubject struct{ Marker }

// ---- тестовый режим ----

type Debug struct{ Marker }
type DebugRole struct {
	Marker
	Role string // пусто или «off» — вернуть свою роль
}
type DebugFinish struct{ Marker }
type DebugJobs struct{ Marker }

// Имена на «проводе» короткие и совпадают с прежними строками,
// поэтому кнопки в уже отправленных сообщениях продолжают работать.
func init() {
	for name, a := range map[string]Action{
		"home": Home{}, "consent": Consent{}, "role": ChooseRole{}, "nameok": KeepProfileName{}, "grade": SetGrade{},
		"st": Settings{}, "stt": ToggleSetting{}, "stok": SettingsDone{},
		"nb": PickSubject{}, "ns": PickTutor{}, "tc": TutorCard{}, "bk": PickDay{}, "bd": PickTime{}, "bt": Book{}, "enr": Enroll{},
		"py": Balances{}, "pay": PayOptions{}, "pp": Purchase{},
		"pinv": InviteParent{}, "fb": Feedback{}, "fbc": FeedbackComment{},
		"ls": StudentLessons{}, "tl": TutorLessons{}, "lo": OpenLesson{}, "rs": RescheduleDay{}, "rd": RescheduleTime{},
		"rt": Reschedule{}, "cx": AskCancel{}, "cxy": Cancel{}, "rx": StopRecurring{}, "cr": DecideRequest{}, "mk": MarkLesson{},
		"kid": Child{}, "kpm": ToggleChildApproval{}, "cinv": InviteChild{}, "kidadd": AddChild{}, "kg": ChildGrade{},
		"tw": Windows{}, "twt": EditWindowsText{}, "twe": AskException{}, "twx": DeleteException{}, "tst": MyStudents{},
		"tp": Profile{}, "tpb": EditBio{}, "tpd": SetDuration{}, "tps": ToggleSubject{},
		"at": AdminTutors{}, "atu": AdminTutor{}, "atp": AskTutorPrice{}, "atpr": ResetTutorPrice{}, "ainv": InviteTutor{},
		"apr": Prices{}, "abp": AskBasePrice{}, "aptg": ToggleProduct{}, "apadd": AskProduct{}, "afb": FeedbackList{},
		"fwd": ForwardFeedback{}, "alc": LateCancels{}, "asub": Subjects{}, "asubadd": AskSubject{},
		"dbg": Debug{}, "dbr": DebugRole{}, "dbf": DebugFinish{}, "dbt": DebugJobs{},
	} {
		register(name, a)
	}
}

// All — по экземпляру каждого действия (для тестов протокола).
func All() []Action {
	out := make([]Action, 0, len(byName))
	for _, t := range byName {
		out = append(out, zeroOf(t))
	}
	return out
}
