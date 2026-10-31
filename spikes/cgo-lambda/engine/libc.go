package engine

/*
#include <gnu/libc-version.h>
*/
import "C"

// LibcVersion is the glibc version the process is running with: the system's
// for a dynamic binary, the one built in for a static one.
func LibcVersion() string {
	return C.GoString(C.gnu_get_libc_version())
}
