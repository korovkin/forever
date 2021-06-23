package forever

import "fmt"

const VERSION_NUMBER = "0000.0001.044"

var VERSION_GIT_HASH string = "?"
var VERSION_DATE string = "?"

func VersionString() string {
	return fmt.Sprintf(
		"ver:%s git:%s time:%s",
		VERSION_NUMBER,
		VERSION_GIT_HASH,
		VERSION_NUMBER)
}
