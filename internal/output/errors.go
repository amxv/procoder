package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/amxv/procoder/internal/errs"
)

func WriteError(w io.Writer, err error) {
	if err == nil {
		return
	}
	_, _ = io.WriteString(w, FormatError(err))
}

func FormatError(err error) string {
	if err == nil {
		return ""
	}

	var b strings.Builder
	if typed, ok := errs.As(err); ok {
		code := typed.Code
		if code == "" {
			code = errs.CodeInternal
		}
		message := typed.Message
		if message == "" {
			message = "unexpected error"
		}
		_, _ = fmt.Fprintf(&b, "%s: %s\n", code, message)
		for _, detail := range typed.Details {
			detail = strings.TrimSpace(detail)
			if detail == "" {
				continue
			}
			_, _ = fmt.Fprintf(&b, "%s\n", detail)
		}
		if typed.Hint != "" {
			_, _ = fmt.Fprintf(&b, "Hint: %s\n", typed.Hint)
		}
		return b.String()
	}

	_, _ = fmt.Fprintf(&b, "%s: %s\n", errs.CodeInternal, err.Error())
	return b.String()
}
