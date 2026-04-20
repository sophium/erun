//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework AppKit
#include <stdlib.h>
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>

static void erunSetProcessName(const char *name) {
	@autoreleasepool {
		NSString *value = [NSString stringWithUTF8String:name];
		[[NSProcessInfo processInfo] setProcessName:value];
	}
}

static void erunConfigureAppIdentity(const char *name) {
	@autoreleasepool {
		NSString *value = [NSString stringWithUTF8String:name];
		[[NSProcessInfo processInfo] setProcessName:value];
		NSApplication *app = [NSApplication sharedApplication];
		NSMenu *mainMenu = [app mainMenu];
		if (mainMenu != nil && [mainMenu numberOfItems] > 0) {
			[[mainMenu itemAtIndex:0] setTitle:value];
		}
	}
}
*/
import "C"

import "unsafe"

func setAppIdentity(name string) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.erunSetProcessName(cname)
}

func configureAppIdentity(name string) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.erunConfigureAppIdentity(cname)
}
