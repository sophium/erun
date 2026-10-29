//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework AppKit
#include <stdlib.h>
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <os/log.h>

// erunObserveActivation logs the call stack each time the app becomes frontmost.
// The system log cannot tell a user switch from self-activation; the stack can.
static void erunObserveActivation(void) {
	@autoreleasepool {
		[[NSNotificationCenter defaultCenter]
		    addObserverForName:NSApplicationDidBecomeActiveNotification
		                object:nil
		                 queue:nil
		            usingBlock:^(NSNotification *note) {
			            (void)note;
			            // %{public}@: os_log redacts dynamic arguments as <private> by default.
			            os_log(OS_LOG_DEFAULT,
			                   "erun-activation: became frontmost; stack=%{public}@",
			                   [NSThread callStackSymbols]);
		            }];
	}
}
*/
import "C"

// observeAppActivation installs the frontmost-activation trace (darwin only).
func observeAppActivation() {
	C.erunObserveActivation()
}
