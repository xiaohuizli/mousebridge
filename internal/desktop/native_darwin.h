#include <stdint.h>
int mb_allowed(void);
int mb_width(void);
int mb_height(void);
int mb_capture(void);
void mb_stop(void);
void mb_reset(void);
int mb_apply(int kind,double x,double y,int button,int down);
