#include <stdint.h>
int mb_allowed(void);
int mb_bounds(int *left,int *top,int *width,int *height);
int mb_capture(void);
void mb_stop(void);
void mb_reset(void);
int mb_apply(int kind,double x,double y,int button,int down);
