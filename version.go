package forever

import "fmt"

var VERSION_NUMBER = "0000.0001.048"
var VERSION_GIT_HASH = ""
var VERSION_COMPILE_TIME = ""

func VersionString() string {
	return fmt.Sprintf(
		"ver:%s git:%s time:%s",
		VERSION_NUMBER,
		VERSION_GIT_HASH,
		VERSION_COMPILE_TIME)
}
