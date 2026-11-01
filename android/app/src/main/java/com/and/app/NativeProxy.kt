package com.and.app

object NativeProxy {
    init {
        System.loadLibrary("byedpi")
    }

    external fun start(args: Array<String>): Int
    external fun stop(): Int
}
