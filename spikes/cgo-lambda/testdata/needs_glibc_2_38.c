// strlcpy was added to glibc in 2.38, so this needs GLIBC_2.38.
#include <stdio.h>
#include <string.h>

int main(void) {
  char buf[8];
  strlcpy(buf, "hello", sizeof buf);
  puts(buf);
  return 0;
}
