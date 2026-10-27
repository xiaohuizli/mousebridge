//go:build darwin

#include "native_darwin.h"
#include <ApplicationServices/ApplicationServices.h>
#include <stdatomic.h>
#include <math.h>
#include <stdlib.h>
extern int goMouseEvent(int kind,double x,double y,double dx,double dy,int button,int down);
static const int64_t marker=0x4d424731;
static atomic_int stopped=0;
static unsigned held=0;
static CFAbsoluteTime lastClickTime[3];
static CGPoint lastClickPoint[3];
static int clickCount[3];
static double wheelX=0,wheelY=0;
static CFMachPortRef tap;
int mb_allowed(void){return CGPreflightPostEventAccess();}
int mb_bounds(int *left,int *top,int *width,int *height){
 uint32_t count=0;
 if(CGGetActiveDisplayList(0,NULL,&count)!=kCGErrorSuccess||count==0)return 1;
 CGDirectDisplayID *displays=calloc(count,sizeof(*displays));if(!displays)return 1;
 if(CGGetActiveDisplayList(count,displays,&count)!=kCGErrorSuccess){free(displays);return 1;}
 CGRect bounds=CGRectNull;
 for(uint32_t i=0;i<count;i++)bounds=CGRectUnion(bounds,CGDisplayBounds(displays[i]));
 free(displays);
 if(CGRectIsNull(bounds)||CGRectIsEmpty(bounds))return 1;
 *left=(int)CGRectGetMinX(bounds);*top=(int)CGRectGetMinY(bounds);
 *width=(int)CGRectGetWidth(bounds);*height=(int)CGRectGetHeight(bounds);
 return 0;
}
void mb_stop(void){atomic_store(&stopped,1);}
void mb_reset(void){atomic_store(&stopped,0);}
static CGEventRef callback(CGEventTapProxy proxy,CGEventType type,CGEventRef event,void *info){
 if(type==kCGEventTapDisabledByTimeout||type==kCGEventTapDisabledByUserInput){goMouseEvent(4,0,0,0,0,0,0);CGEventTapEnable(tap,true);return event;}
 if(CGEventGetIntegerValueField(event,kCGEventSourceUserData)==marker)return event;
 CGPoint p=CGEventGetLocation(event);int kind=0,button=0,down=0;double dx=0,dy=0;
 switch(type){
 case kCGEventMouseMoved:case kCGEventLeftMouseDragged:case kCGEventRightMouseDragged:case kCGEventOtherMouseDragged:
  kind=1;dx=CGEventGetIntegerValueField(event,kCGMouseEventDeltaX);dy=CGEventGetIntegerValueField(event,kCGMouseEventDeltaY);break;
 case kCGEventLeftMouseDown:case kCGEventRightMouseDown:case kCGEventOtherMouseDown:down=1; // fallthrough
 case kCGEventLeftMouseUp:case kCGEventRightMouseUp:case kCGEventOtherMouseUp:
  kind=2;button=(int)CGEventGetIntegerValueField(event,kCGMouseEventButtonNumber);if(button>2)return event;break;
 case kCGEventScrollWheel:kind=3;dy=CGEventGetDoubleValueField(event,kCGScrollWheelEventFixedPtDeltaAxis1);dx=-CGEventGetDoubleValueField(event,kCGScrollWheelEventFixedPtDeltaAxis2);break;
 case kCGEventKeyDown:if(CGEventGetIntegerValueField(event,kCGKeyboardEventKeycode)==53)kind=4;break;
 default:break;
 }
 if(kind&&goMouseEvent(kind,p.x,p.y,dx,dy,button,down))return NULL;return event;
}
int mb_capture(void){
 if(!CGPreflightListenEventAccess()||!CGPreflightPostEventAccess())return 1;
 CGEventMask mask=CGEventMaskBit(kCGEventMouseMoved)|CGEventMaskBit(kCGEventLeftMouseDragged)|CGEventMaskBit(kCGEventRightMouseDragged)|CGEventMaskBit(kCGEventOtherMouseDragged)|CGEventMaskBit(kCGEventLeftMouseDown)|CGEventMaskBit(kCGEventLeftMouseUp)|CGEventMaskBit(kCGEventRightMouseDown)|CGEventMaskBit(kCGEventRightMouseUp)|CGEventMaskBit(kCGEventOtherMouseDown)|CGEventMaskBit(kCGEventOtherMouseUp)|CGEventMaskBit(kCGEventScrollWheel)|CGEventMaskBit(kCGEventKeyDown);
 tap=CGEventTapCreate(kCGSessionEventTap,kCGHeadInsertEventTap,kCGEventTapOptionDefault,mask,callback,NULL);if(!tap)return 2;
 CFRunLoopSourceRef src=CFMachPortCreateRunLoopSource(NULL,tap,0);if(!src){CFRelease(tap);return 3;}
 CFRunLoopAddSource(CFRunLoopGetCurrent(),src,kCFRunLoopCommonModes);CGEventTapEnable(tap,true);
 while(!atomic_load(&stopped))CFRunLoopRunInMode(kCFRunLoopDefaultMode,0.05,false);
 CGEventTapEnable(tap,false);CFRunLoopRemoveSource(CFRunLoopGetCurrent(),src,kCFRunLoopCommonModes);CFRelease(src);CFRelease(tap);tap=NULL;return 0;
}
int mb_apply(int kind,double x,double y,int button,int down){
 CGEventSourceRef source=CGEventSourceCreate(kCGEventSourceStatePrivate);if(!source)return 1;CGEventSourceSetUserData(source,marker);
 CGEventRef e=NULL;
 if(kind==1){
  CGEventType t=kCGEventMouseMoved;CGMouseButton b=kCGMouseButtonLeft;
  if(held&1){t=kCGEventLeftMouseDragged;}else if(held&2){t=kCGEventRightMouseDragged;b=kCGMouseButtonRight;}else if(held&4){t=kCGEventOtherMouseDragged;b=kCGMouseButtonCenter;}
  e=CGEventCreateMouseEvent(source,t,CGPointMake(x,y),b);
 }else if(kind==2){
  CGEventRef current=CGEventCreate(NULL);if(!current){CFRelease(source);return 1;}CGPoint p=CGEventGetLocation(current);CFRelease(current);
  CGEventType t=button==0?(down?kCGEventLeftMouseDown:kCGEventLeftMouseUp):button==1?(down?kCGEventRightMouseDown:kCGEventRightMouseUp):(down?kCGEventOtherMouseDown:kCGEventOtherMouseUp);
  if(down)held|=1u<<button;else held&=~(1u<<button);
  if(down){
   CFAbsoluteTime now=CFAbsoluteTimeGetCurrent();
   if(now-lastClickTime[button]<=0.5&&fabs(p.x-lastClickPoint[button].x)<=4&&fabs(p.y-lastClickPoint[button].y)<=4){clickCount[button]=clickCount[button]%3+1;}else{clickCount[button]=1;}
   lastClickTime[button]=now;lastClickPoint[button]=p;
  }
  e=CGEventCreateMouseEvent(source,t,p,(CGMouseButton)button);
  if(e)CGEventSetIntegerValueField(e,kCGMouseEventClickState,clickCount[button]);
 }else if(kind==3){wheelX+=x;wheelY+=y;int32_t ix=(int32_t)wheelX,iy=(int32_t)wheelY;wheelX-=ix;wheelY-=iy;e=CGEventCreateScrollWheelEvent(source,kCGScrollEventUnitLine,2,iy,-ix);}
 if(!e){CFRelease(source);return 1;}CGEventSetIntegerValueField(e,kCGEventSourceUserData,marker);CGEventPost(kCGHIDEventTap,e);CFRelease(e);CFRelease(source);return 0;
}
