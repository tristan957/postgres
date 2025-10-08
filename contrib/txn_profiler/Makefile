# contrib/txn_profiler/Makefile

MODULE_big = txn_profiler
OBJS = \
	$(WIN32RES) \
	txn_profile.o \
	txn_profiler.o

EXTENSION = txn_profiler
DATA = txn_profiler--1.0.sql

ifdef USE_PGXS
PG_CONFIG = pg_config
PGXS := $(shell $(PG_CONFIG) --pgxs)
include $(PGXS)
else
subdir = contrib/txn_profiler
top_builddir = ../..
include $(top_builddir)/src/Makefile.global
include $(top_srcdir)/contrib/contrib-global.mk
endif
