package shortcut

import "fmt"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Exit    int    `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func Fail(code string, exit int, format string, args ...any) error {
	return &Error{code, fmt.Sprintf(format, args...), exit}
}
