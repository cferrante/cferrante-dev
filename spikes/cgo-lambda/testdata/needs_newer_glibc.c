// Needs two glibc versions newer than Lambda's 2.34: arc4random (2.36) and
// strlcpy (2.38). The checker must report both, not just the newest.
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(void) {
  char buf[8];
  strlcpy(buf, "hello", sizeof buf);
  printf("%s %u\n", buf, arc4random());
  return 0;
}
