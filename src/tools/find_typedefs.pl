#!/usr/bin/perl

# src/tools/find_typedef

# This script attempts to find all typedef's in the postgres binaries
# by using 'objdump' or local equivalent to print typedef debugging symbols.
# We need this because pgindent needs a list of typedef names.
#
# For this program to work, you must have compiled all code with
# debugging symbols.
#
# We intentionally examine all files in the targeted directories so as to
# find both .o files and executables.  Therefore, ignore error messages about
# unsuitable files being fed to objdump.
#
# This is known to work on Linux and on some BSDen, including macOS.
#
# Caution: on the platforms we use, this only prints typedefs that are used
# to declare at least one variable or struct field.  If you have say
# "typedef struct foo { ... } foo;", and then the structure is only ever
# referenced as "struct foo", "foo" will not be reported as a typedef,
# causing pgindent to indent the typedef definition oddly.  This is not a
# huge problem, since by definition there's just the one misindented line.
#
# We get typedefs by reading "STABS":
#    http://www.informatik.uni-frankfurt.de/doc/texi/stabs_toc.html

use warnings;
use strict;

use Config;
use File::Find ();

if (@ARGV != 1)
{
	print { *STDERR } "Usage: $0 postgres_install_directory [...]\n";
	exit 1;
}

my $installdir = $ARGV[0];

# replace previous use of external egrep -A
sub _dump_filter
{
	my ($lines, $tag, $context) = @_;
	my @output;
	while (@$lines)
	{
		my $line = shift @$lines;
		if (index($line, $tag) > -1)
		{
			push(@output, splice(@$lines, 0, $context));
		}
	}
	return @output;
}

# work around the fact that ucrt/binutils objdump is far slower
# than the one in msys/binutils
local $ENV{PATH} = $ENV{PATH};
$ENV{PATH} = "/usr/bin:$ENV{PATH}" if $Config{osname} eq 'msys';

my $objdump = 'objdump';

my @err = `$objdump -W 2>&1`;
my @readelferr = `readelf -w 2>&1`;
my $using_osx = (`uname` eq "Darwin\n");
my @testfiles;
my %syms;
my @dumpout;
my @flds;

@testfiles = (
	glob("$installdir/bin/*"),
	glob("$installdir/lib/*"),
	glob("$installdir/lib/postgresql/*")
);
foreach my $bin (@testfiles)
{
	next if $bin =~ m!bin/(ipcclean|pltcl_)!;
	next unless -f $bin;
	next if -l $bin;                        # ignore symlinks to plain files
	next if $bin =~ m!/postmaster.exe$!;    # sometimes a copy not a link

	if ($using_osx)
	{
		# Creates a $bin.dwarf file
		`dsymutil --flat $bin 2>/dev/null`;
		if ($? != 0)
		{
			# We might be here if analyzing static library files for instance
			continue;
		}

		@dumpout = `dwarfdump $bin 2>/dev/null`;
		unlink "$bin.dwarf";

		@dumpout = _dump_filter(\@dumpout, 'TAG_typedef', 2);
		foreach (@dumpout)
		{
			## no critic (RegularExpressions::ProhibitCaptureWithoutTest)
			@flds = split;
			if (@flds == 3)
			{
				# old format
				next unless ($flds[0] eq "AT_name(");
				next unless ($flds[1] =~ m/^"(.*)"$/);
				$syms{$1} = 1;
			}
			elsif (@flds == 2)
			{
				# new format
				next unless ($flds[0] eq "DW_AT_name");
				next unless ($flds[1] =~ m/^\("(.*)"\)$/);
				$syms{$1} = 1;
			}
		}
	}
	elsif (@err == 1)    # Linux and sometimes windows
	{
		my $cmd = "$objdump -Wi $bin 2>/dev/null";
		@dumpout = `$cmd`;
		@dumpout = _dump_filter(\@dumpout, 'DW_TAG_typedef', 3);
		foreach (@dumpout)
		{
			@flds = split;
			next unless (1 < @flds);
			next
				if (($flds[0] ne 'DW_AT_name' && $flds[1] ne 'DW_AT_name')
				|| $flds[-1] =~ /^DW_FORM_str/);
			$syms{ $flds[-1] } = 1;
		}
	}
	elsif (@readelferr > 10)
	{
		# FreeBSD, similar output to Linux
		my $cmd = "readelf -w $bin 2>/dev/null";
		@dumpout = ` $cmd`;
		@dumpout = _dump_filter(\@dumpout, 'DW_TAG_typedef', 3);

		foreach (@dumpout)
		{
			@flds = split;
			next unless (1 < @flds);
			next if ($flds[0] ne 'DW_AT_name');
			$syms{ $flds[-1] } = 1;
		}
	}
	else
	{
		@dumpout = `$objdump --stabs $bin 2>/dev/null`;
		foreach (@dumpout)
		{
			@flds = split;
			next if (@flds < 7);
			next if ($flds[1] ne 'LSYM' || $flds[6] !~ /([^:]+):t/);
			## no critic (RegularExpressions::ProhibitCaptureWithoutTest)
			$syms{$1} = 1;
		}
	}
}
my @badsyms = grep { /\s/ } keys %syms;
push(@badsyms, 'date', 'interval', 'timestamp', 'ANY');
delete @syms{@badsyms};

my @goodsyms = sort keys %syms;
print join("\n", @goodsyms);
print "\n";
