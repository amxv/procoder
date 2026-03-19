package errs

import (
	stderrors "errors"
	"fmt"
)

type Code string

const (
	CodeNotGitRepo             Code = "NOT_GIT_REPO"
	CodeWorktreeDirty          Code = "WORKTREE_DIRTY"
	CodeUntrackedFilesPresent  Code = "UNTRACKED_FILES_PRESENT"
	CodeSubmodulesUnsupported  Code = "SUBMODULES_UNSUPPORTED"
	CodeLFSUnsupported         Code = "LFS_UNSUPPORTED"
	CodeInvalidExchange        Code = "INVALID_EXCHANGE"
	CodeInvalidReturnPackage   Code = "INVALID_RETURN_PACKAGE"
	CodeBundleVerifyFailed     Code = "BUNDLE_VERIFY_FAILED"
	CodeRefOutOfScope          Code = "REF_OUT_OF_SCOPE"
	CodeNoNewCommits           Code = "NO_NEW_COMMITS"
	CodeBranchMoved            Code = "BRANCH_MOVED"
	CodeRefExists              Code = "REF_EXISTS"
	CodeTargetBranchCheckedOut Code = "TARGET_BRANCH_CHECKED_OUT"
	CodeUnknownCommand         Code = "UNKNOWN_COMMAND"
	CodeNotImplemented         Code = "NOT_IMPLEMENTED"
	CodeGitCommandFailed       Code = "GIT_COMMAND_FAILED"
	CodeGitUnavailable         Code = "GIT_UNAVAILABLE"
	CodeInternal               Code = "INTERNAL"
)

type Error struct {
	Code    Code
	Message string
	Hint    string
	Details []string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Option func(*Error)

func New(code Code, message string, opts ...Option) *Error {
	e := &Error{
		Code:    code,
		Message: message,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func Wrap(code Code, message string, err error, opts ...Option) *Error {
	opts = append([]Option{WithCause(err)}, opts...)
	return New(code, message, opts...)
}

func WithHint(hint string) Option {
	return func(e *Error) {
		e.Hint = hint
	}
}

func WithDetails(details ...string) Option {
	return func(e *Error) {
		e.Details = append(e.Details, details...)
	}
}

func WithDetailf(format string, args ...any) Option {
	return WithDetails(fmt.Sprintf(format, args...))
}

func WithCause(err error) Option {
	return func(e *Error) {
		e.Err = err
	}
}

func As(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	var typed *Error
	if stderrors.As(err, &typed) {
		return typed, true
	}
	return nil, false
}

func CodeOf(err error) Code {
	if typed, ok := As(err); ok {
		if typed.Code != "" {
			return typed.Code
		}
	}
	return CodeInternal
}
