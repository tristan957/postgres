# Copyright (c) 2025, PostgreSQL Global Development Group

txn_profiler_sources = files(
  'txn_profiler.c',
  'txn_profile.c',
)

if host_system == 'windows'
  txn_profiler_sources += rc_lib_gen.process(win32ver_rc, extra_args: [
    '--NAME', 'txn_profiler',
    '--FILEDESC', 'Transaction profiling',])
endif

txn_profiler = shared_module('txn_profiler',
  txn_profiler_sources,
  kwargs: contrib_mod_args,
)
contrib_targets += txn_profiler

install_data(
  'txn_profiler--1.0.sql',
  'txn_profiler.control',
  kwargs: contrib_data_args,
)
