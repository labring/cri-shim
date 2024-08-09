package errors

import "fmt"

var ErrPasswordNotFound = fmt.Errorf("password not found")

var ErrContainerAlreadyCommit = fmt.Errorf("container already commit")
