package actions

import (
	"errors"
	"reflect"
	"testing"
)

// fill заполняет поля «реалистично большими» значениями: id до миллиарда,
// время — unix-секунды, перечисления — допустимыми значениями.
func fill(a Action) Action {
	v := reflect.New(reflect.TypeOf(a)).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch v.Type().Field(i).Type {
		case reflect.TypeOf(BookMode("")):
			f.SetString(string(ModeRecurring))
		case reflect.TypeOf(Mark("")):
			f.SetString(string(MarkNoShow))
		case reflect.TypeOf(Day("")):
			f.SetString("20261231")
		default:
			switch f.Kind() {
			case reflect.Int64:
				f.SetInt(999_999_999)
			case reflect.Int:
				f.SetInt(99_999)
			case reflect.Bool:
				f.SetBool(true)
			case reflect.String:
				f.SetString("student")
			}
		}
	}
	return v.Interface().(Action)
}

func TestRoundTripAllActions(t *testing.T) {
	all := All()
	if len(all) < 60 {
		t.Fatalf("зарегистрировано подозрительно мало действий: %d", len(all))
	}
	for _, zero := range all {
		a := fill(zero)
		s := Encode(a) // паника = поле не поддерживается или строка длиннее 64 байт
		if len(s) > MaxLen {
			t.Errorf("%T: %d байт", a, len(s))
		}
		got, err := Decode(s)
		if err != nil {
			t.Errorf("%T: Decode(%q): %v", a, s, err)
			continue
		}
		if !reflect.DeepEqual(got, a) {
			t.Errorf("%T: после кодирования %q получили %#v", a, s, got)
		}
	}
}

func TestWireNamesStable(t *testing.T) {
	// эти строки уже лежат в кнопках отправленных сообщений — менять нельзя
	cases := map[string]Action{
		"mk:42:held":    MarkLesson{LessonID: 42, Mark: MarkHeld},
		"fb:7:1":        Feedback{LessonID: 7, Liked: true},
		"cr:3:0":        DecideRequest{RequestID: 3, Approve: false},
		"bk:5:1:9:t":    PickDay{TutorID: 5, SubjectID: 1, StudentID: 9, Mode: ModeTrial},
		"pay:9:5":       PayOptions{StudentID: 9, TutorID: 5},
		"fwd:11":        ForwardFeedback{FeedbackID: 11},
		"home":          Home{},
		"dbr:student":   DebugRole{Role: "student"},
		"rd:8:20261006": RescheduleTime{LessonID: 8, Day: "20261006"},
	}
	for wire, a := range cases {
		if got := Encode(a); got != wire {
			t.Errorf("Encode(%#v) = %q, ожидалось %q", a, got, wire)
		}
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	for _, s := range []string{
		"",                         // пусто
		"nope:1",                   // неизвестное имя
		"mk:42",                    // не хватает аргумента
		"mk:42:held:extra",         // лишний аргумент
		"mk:x:held",                // не число
		"mk:42:delete",             // недопустимая отметка
		"fb:7:yes",                 // не 0/1
		"bk:5:1:9:z",               // недопустимый режим
		"rd:8:2026-10-06",          // неверный день
		"rd:8:20261340",            // несуществующая дата
		"tpd:99999999999999999999", // переполнение int
		"home:",                    // аргумент у действия без аргументов
	} {
		if a, err := Decode(s); err == nil {
			t.Errorf("Decode(%q) = %#v, ожидалась ошибка", s, a)
		} else if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrUnknown) {
			t.Errorf("Decode(%q): неожиданный вид ошибки %v", s, err)
		}
	}
}

func TestEncodeRefusesColon(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("двоеточие в строковом поле должно паниковать: иначе Decode разберёт кнопку неверно")
		}
	}()
	Encode(DebugRole{Role: "a:b"})
}
