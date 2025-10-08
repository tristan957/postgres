package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

func main() {
	connStr := flag.String("db", "postgres://localhost/postgres?host=/tmp", "PostgreSQL connection string")
	inputDir := flag.String("input", "", "Input directory containing *.bin files")
	inputFiles := flag.String("files", "", "Comma-separated list of input files")
	output := flag.String("output", "trace.json", "Output Perfetto trace file")
	flag.Parse()

	ctx := context.Background()

	var files []string

	// Collect input files
	if *inputDir != "" {
		matches, err := filepath.Glob(filepath.Join(*inputDir, "txn_profile_*.bin"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error globbing: %v\n", err)
			os.Exit(1)
		}
		files = matches
	} else if *inputFiles != "" {
		// Parse comma-separated files
		files = strings.Split(*inputFiles, ",")
		for i := range files {
			files[i] = strings.TrimSpace(files[i])
		}
	} else if len(flag.Args()) > 0 {
		files = flag.Args()
	} else {
		fmt.Fprintf(os.Stderr, "Usage: perfetto-converter -input <dir> -output <file>\n")
		fmt.Fprintf(os.Stderr, "   or: perfetto-converter file1.bin file2.bin ... -output <file>\n")
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "No input files found\n")
		os.Exit(1)
	}

	fmt.Printf("Processing %d files...\n", len(files))

	// Parse all files
	var profileFiles []*ProfileFile
	for _, filename := range files {
		pf, err := ParseProfileFile(filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", filename, err)
			continue
		}
		profileFiles = append(profileFiles, pf)
	}

	if len(profileFiles) == 0 {
		fmt.Fprintf(os.Stderr, "No files successfully parsed\n")
		os.Exit(1)
	}

	// Connect to PostgreSQL
	log.Printf("Connecting to PostgreSQL...")
	conn, err := pgx.Connect(ctx, *connStr)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer conn.Close(ctx)

	// Collect pg_tracing spans
	filters := FilterOptions{}
	log.Printf("Querying pg_tracing spans...")
	spans, err := queryPgTracingSpans(ctx, conn, filters)
	if err != nil {
		log.Panicf("Failed to query spans: %v", err)
	}

	log.Printf("Total spans queried: %d", len(spans))

	if err := GeneratePerfettoTraceFromSpans(spans, *output); err != nil {
		log.Panicf("Failed to generate trace: %v", err)
	}

	// Generate Perfetto trace
	/*if err := GeneratePerfettoTrace(profileFiles, *output); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating trace: %v\n", err)
		os.Exit(1)
	}*/

	fmt.Printf("Trace written to %s\n", *output)
	fmt.Printf("\nView in Chrome: chrome://tracing\n")
	fmt.Printf("Or visit: https://ui.perfetto.dev/\n")
}
