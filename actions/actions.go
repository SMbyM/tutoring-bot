// Package actions — протокол кнопок: каждое действие — отдельный тип с полями,
// а не строка с позиционными аргументами. Encode/Decode — единственное место,
// где действие превращается в строку (callback_data) и обратно.
//
// Данные кнопки приходят от клиента, поэтому Decode строгий: неизвестное имя,
// неверное число аргументов, нечисловое значение или недопустимый режим — ошибка.
// Права на само действие проверяет core.
package actions

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// MaxLen — лимит Telegram на callback_data.
const MaxLen = 64

// Action — любое действие кнопки. Реализуют только типы этого пакета.
type Action interface{ isAction() }

// Marker встраивается в каждое действие и делает интерфейс «закрытым».
type Marker struct{}

func (Marker) isAction() {}

var (
	ErrUnknown = errors.New("неизвестное действие")
	ErrInvalid = errors.New("некорректные данные кнопки")
)

var (
	byName = map[string]reflect.Type{}
	nameOf = map[reflect.Type]string{}
)

func register(name string, a Action) {
	t := reflect.TypeOf(a)
	if _, dup := byName[name]; dup {
		panic("actions: имя занято дважды: " + name)
	}
	byName[name], nameOf[t] = t, name
}

// Encode превращает действие в строку. Ошибки здесь — ошибки программиста
// (незарегистрированный тип, двоеточие в строке, превышение лимита), поэтому panic.
func Encode(a Action) string {
	v := reflect.ValueOf(a)
	name, ok := nameOf[v.Type()]
	if !ok {
		panic(fmt.Sprintf("actions: тип %T не зарегистрирован", a))
	}
	parts := []string{name}
	for i := 0; i < v.NumField(); i++ {
		if v.Type().Field(i).Anonymous {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Int, reflect.Int64:
			parts = append(parts, strconv.FormatInt(f.Int(), 10))
		case reflect.Bool:
			parts = append(parts, map[bool]string{true: "1", false: "0"}[f.Bool()])
		case reflect.String:
			s := f.String()
			if strings.ContainsRune(s, ':') {
				panic(fmt.Sprintf("actions: двоеточие в строковом поле %T", a))
			}
			parts = append(parts, s)
		default:
			panic(fmt.Sprintf("actions: неподдерживаемое поле в %T", a))
		}
	}
	out := strings.Join(parts, ":")
	if len(out) > MaxLen {
		panic(fmt.Sprintf("actions: %q длиннее %d байт", out, MaxLen))
	}
	return out
}

// Decode разбирает строку кнопки в действие.
func Decode(s string) (Action, error) {
	if len(s) > MaxLen {
		return nil, ErrInvalid
	}
	parts := strings.Split(s, ":")
	t, ok := byName[parts[0]]
	if !ok {
		return nil, ErrUnknown
	}
	v := reflect.New(t).Elem()
	args := parts[1:]
	n := 0
	for i := 0; i < v.NumField(); i++ {
		if t.Field(i).Anonymous {
			continue
		}
		if n >= len(args) {
			return nil, ErrInvalid
		}
		raw := args[n]
		n++
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Int, reflect.Int64:
			x, err := strconv.ParseInt(raw, 10, f.Type().Bits())
			if err != nil {
				return nil, ErrInvalid
			}
			f.SetInt(x)
		case reflect.Bool:
			switch raw {
			case "1":
				f.SetBool(true)
			case "0":
			default:
				return nil, ErrInvalid
			}
		case reflect.String:
			f.SetString(raw)
		}
	}
	if n != len(args) {
		return nil, ErrInvalid
	}
	a := v.Interface().(Action)
	if val, ok := a.(interface{ validate() error }); ok {
		if err := val.validate(); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func zeroOf(t reflect.Type) Action { return reflect.New(t).Elem().Interface().(Action) }
