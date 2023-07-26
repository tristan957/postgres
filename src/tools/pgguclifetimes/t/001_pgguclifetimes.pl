# pgguclifetimes: some simple tests

# Copyright (c) 2023, PostgreSQL Global Development Group

use strict;
use warnings;

use Cwd qw(getcwd);

use PostgreSQL::Test::Utils;
use Test::More;

my $workdir = getcwd;

program_help_ok('pgguclifetimes');
program_options_handling_ok('pgguclifetimes');

command_fails(
	['pgguclifetimes'],
	'pgguclifetimes without a compilation database arg');

command_fails(
	['pgguclifetimes', "$workdir/does-not-exist"],
	'pgguclifetimes with a non-existent compilation database directory');

command_ok(
	['pgguclifetimes', "$workdir/tests/success"],
	'pgguclifetimes exits with 0 after analyzing a fully annotated codebase');

command_checks_all(
	['pgguclifetimes', "$workdir/tests/failure"],
	1,
	[qr/^$/],
	[
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:5:5: needs_all is not marked as thread-local storage/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:5:5: needs_all is missing a lifetime annotation/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:6:12: static_needs_all is not marked as thread-local storage/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:6:12: static_needs_all is missing a lifetime annotation/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:7:12: extern_needs_all is not marked as thread-local storage/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:7:12: extern_needs_all is missing a lifetime annotation/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:9:14: needs_annotation is missing a lifetime annotation/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:10:21: static_needs_annotation is missing a lifetime annotation/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:11:21: extern_needs_annotation is missing a lifetime annotation/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:13:53: needs_thread is not marked as thread-local storage/,
		qr/src\/tools\/pgguclifetimes\/tests\/failure\/example.c:14:57: static_needs_thread is not marked as thread-local storage/,
	],
	'pgguclifetimes analyzing a non-annotated codebase'
);

done_testing();
