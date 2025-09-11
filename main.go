package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/spf13/cobra"
)

func main() {
	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var opt struct {
	Region      string
	NumToKeep   int
	Concurrency int
	DryRun      bool
}

var cmd = &cobra.Command{
	Use:  "lambda-clean function-name-prefix",
	Args: cobra.ExactArgs(1),
	Run:  run,
}

func init() {
	cmd.Flags().StringVarP(&opt.Region, "region", "r", "", "AWS Region (default \"default\" from local configuration)")
	cmd.Flags().IntVarP(&opt.NumToKeep, "num-to-keep", "n", 2, "Number of latest versions to keep. Older versions will be deleted.")
	cmd.Flags().IntVarP(&opt.Concurrency, "concurrency", "c", 5, "Number of delete requests that can be performed concurrently.")
	cmd.Flags().BoolVar(&opt.DryRun, "dry-run", false, "If this option is specified, the function version is not deleted.")
}

func run(cmd *cobra.Command, args []string) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithAssumeRoleCredentialOptions(func(options *stscreds.AssumeRoleOptions) {
		options.TokenProvider = func() (string, error) {
			return stscreds.StdinTokenProvider()
		}
	}))
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	if opt.Region != "" {
		cfg.Region = opt.Region
	}

	ch := make(chan func(), opt.Concurrency)

	for range opt.Concurrency {
		go func() {
			for f := range ch {
				f()
			}
		}()
	}

	cli := lambda.NewFromConfig(cfg, func(options *lambda.Options) {
		options.RetryMaxAttempts = 8
		options.RetryMode = aws.RetryModeAdaptive
	})

	for fn := range listFunctions(ctx, cli, args[0]) {
		vers := make([]string, 0, opt.NumToKeep+1)

		var wg sync.WaitGroup

		for ver := range listVersions(ctx, cli, fn) {
			vers = append(vers, ver)

			if len(vers) > opt.NumToKeep {
				ver, vers = vers[0], vers[1:]

				wg.Add(1)
				ch <- func() {
					defer wg.Done()
					if !opt.DryRun {
						deleteVersion(ctx, cli, fn, ver)
					}
					fmt.Printf("[DELETE] %s:%s\n", fn, ver)
				}
			}
		}

		wg.Wait()

		for _, ver := range vers {
			fmt.Printf("[KEEP]   %s:%s\n", fn, ver)
		}
	}

	close(ch)
}

func listFunctions(ctx context.Context, cli *lambda.Client, prefix string) <-chan string {
	ch := make(chan string)

	go func() {
		defer close(ch)

		var marker *string

		for {
			out, err := cli.ListFunctions(ctx, &lambda.ListFunctionsInput{Marker: marker})
			if errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			if err != nil {
				log.Fatalf("failed to list Lambda functions: %v", err)
			}

			for _, fc := range out.Functions {
				if strings.HasPrefix(*fc.FunctionName, prefix) {
					ch <- *fc.FunctionName
				}
			}

			if out.NextMarker == nil {
				break
			}
			marker = out.NextMarker
		}
	}()

	return ch
}

func listVersions(ctx context.Context, cli *lambda.Client, functionName string) <-chan string {
	ch := make(chan string)

	go func() {
		defer close(ch)

		var marker *string

		for {
			lo, err := cli.ListVersionsByFunction(ctx, &lambda.ListVersionsByFunctionInput{
				FunctionName: aws.String(functionName),
				Marker:       marker,
			})
			if errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			if err != nil {
				log.Fatalf("failed to list Lambda function versions: %v", err)
			}

			for _, fv := range lo.Versions {
				if !strings.HasPrefix(*fv.Version, "$") {
					ch <- *fv.Version
				}
			}

			if lo.NextMarker == nil {
				break
			}
			marker = lo.NextMarker
		}
	}()

	return ch
}

func deleteVersion(ctx context.Context, cli *lambda.Client, functionName, version string) {
	_, err := cli.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: aws.String(functionName),
		Qualifier:    aws.String(version),
	})
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	if err != nil {
		log.Fatalf("failed to delete Lambda function: %v", err)
	}
}
