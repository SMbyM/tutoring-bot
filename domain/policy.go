package domain

import "time"

// LateWindow — перенос/отмена позже этого срока до начала считается поздней:
// разрешена, но требует причину и помечается в истории.
const LateWindow = 12 * time.Hour

// IsLate сообщает, попадает ли изменение урока в «позднее» окно.
func IsLate(now, start time.Time) bool {
	return start.Sub(now) < LateWindow
}

// AutoHeldAfter — если репетитор не отметил урок, через столько после окончания он считается состоявшимся.
const AutoHeldAfter = 24 * time.Hour

// MinBookingLead — нельзя записаться ближе чем за это время до начала.
const MinBookingLead = time.Hour

// Actor описывает того, кто меняет урок, относительно этого урока.
type Actor struct {
	UserID   int64
	Role     Role
	IsParent bool // родитель ученика этого урока
	IsSelf   bool // сам ученик урока
	IsTutor  bool // репетитор урока
}

// ChangeDecision — что делать с запросом на перенос/отмену.
type ChangeDecision int

const (
	ChangeForbidden     ChangeDecision = iota
	ChangeApply                        // применить сразу
	ChangeNeedsApproval                // ученику нужен «ок» родителя
)

// DecideChange — правило «родитель приоритетнее ученика».
// needsParent — настройка ученика (по умолчанию true), hasParents — есть ли привязанные родители.
func DecideChange(a Actor, needsParent, hasParents bool) ChangeDecision {
	switch {
	case a.Role == RoleAdmin, a.IsTutor, a.IsParent:
		return ChangeApply
	case a.IsSelf:
		if needsParent && hasParents {
			return ChangeNeedsApproval
		}
		return ChangeApply
	}
	return ChangeForbidden
}
