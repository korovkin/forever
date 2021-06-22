package forever

const VERSION_NUMBER = "0000.0001.038"

var VERSION_GIT_HASH string = "?"
var VERSION_DATE string = "?"

func VersionString() string {
	return "date: " + VERSION_DATE +
		" hash: " + VERSION_GIT_HASH +
		" ver: " + VERSION_NUMBER
}
