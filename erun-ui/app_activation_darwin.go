//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework AppKit
#include <stdlib.h>
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <os/log.h>

// erunObserveActivation records every time this app becomes the frontmost
// application, with the in-process call stack at that moment.
//
// Why a backtrace and not a counter: macOS attributes a front-process change to
// WindowServer and launchservicesd in the unified log whether the user clicked
// the app or the app asked to be raised, so the system log alone cannot tell
// "the operator switched to us" from "we stole focus". The call stack can: an
// activation we caused runs on our own thread with our frames underneath it,
// while one the user caused arrives with nothing of ours below AppKit.
//
// This exists because erun was observed taking focus back roughly two seconds
// after the operator switched away, repeatedly, and nothing in erun's own
// source raises the window -- no WindowShow, no activateIgnoringOtherApps, no
// bell handler. Rather than keep guessing, make the app say who did it.
static void erunObserveActivation(void) {
	@autoreleasepool {
		[[NSNotificationCenter defaultCenter]
		    addObserverForName:NSApplicationDidBecomeActiveNotification
		                object:nil
		                 queue:nil
		            usingBlock:^(NSNotification *note) {
			            (void)note;
			            // %{public}@, not NSLog: os_log treats every dynamic
			            // argument as private by default, so the stack this
			            // trace exists to capture came out as
			            // "stack=<private>" and told us nothing. Un-redacting
			            // afterwards needs a configuration profile, so the
			            // only thing that works is marking it public here.
			            os_log(OS_LOG_DEFAULT,
			                   "erun-activation: became frontmost; stack=%{public}@",
			                   [NSThread callStackSymbols]);
		            }];
	}
}
*/
import "C"

// observeAppActivation installs the frontmost-activation trace. Darwin only:
// it is the only platform where the focus theft was reported, and the only one
// with an AppKit notification to hang it on.
func observeAppActivation() {
	C.erunObserveActivation()
}
