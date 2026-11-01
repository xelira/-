LOCAL_PATH := $(call my-dir)

include $(CLEAR_VARS)

BYEDPI_SRC := $(patsubst $(LOCAL_PATH)/%,%,$(wildcard $(LOCAL_PATH)/byedpi/*.c))
BYEDPI_SRC := $(filter-out byedpi/win_service.c,$(BYEDPI_SRC))

LOCAL_MODULE := byedpi
LOCAL_SRC_FILES := $(BYEDPI_SRC) byedpi_jni.c
LOCAL_C_INCLUDES := $(LOCAL_PATH)/byedpi
LOCAL_CFLAGS := -D_DEFAULT_SOURCE -DANDROID_APP -std=c99 -O2 -Wall -Wextra -Wno-unused -Wno-unused-parameter
LOCAL_LDLIBS := -llog -landroid -ldl
LOCAL_LDFLAGS := -Wl,--wrap=daemon -Wl,-z,max-page-size=16384
include $(BUILD_SHARED_LIBRARY)
