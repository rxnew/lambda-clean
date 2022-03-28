package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
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
	Region    string
	NumToKeep int
	DryRun    bool
}

var cmd = &cobra.Command{
	Use:  "aws-lambda-storage-clean function-name-prefix",
	Args: cobra.ExactArgs(1),
	Run:  run,
}

func init() {
	cmd.Flags().StringVarP(&opt.Region, "region", "r", "", "AWS Region (default \"default\" from local configuration)")
	cmd.Flags().IntVarP(&opt.NumToKeep, "num-to-keep", "n", 2, "Number of latest versions to keep. Older versions will be deleted.")
	cmd.Flags().BoolVar(&opt.DryRun, "dry-run", false, "If this option is specified, the function version is not deleted.")
}

func run(cmd *cobra.Command, args []string) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	if opt.Region != "" {
		cfg.Region = opt.Region
	}

	cli := lambda.NewFromConfig(cfg)

	ch := listFunctions(ctx, cli, args[0])

	for {
		fn, ok := <-ch
		if !ok {
			break
		}

		cleanUpVersions(ctx, cli, fn, opt.NumToKeep, opt.DryRun)
	}
}

func listFunctions(ctx context.Context, cli *lambda.Client, prefix string) <-chan string {
	ch := make(chan string)

	go func() {
		defer close(ch)

		var marker *string

		for {
			out, err := cli.ListFunctions(ctx, &lambda.ListFunctionsInput{Marker: marker})
			if ctx.Err() == context.Canceled {
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

func cleanUpVersions(ctx context.Context, cli *lambda.Client, functionName string, numToKeep int, dryRun bool) {
	var (
		versions []string
		marker   *string
	)

	for {
		lo, err := cli.ListVersionsByFunction(ctx, &lambda.ListVersionsByFunctionInput{
			FunctionName: aws.String(functionName),
			Marker:       marker,
		})
		if ctx.Err() == context.Canceled {
			return
		}
		if err != nil {
			log.Fatalf("failed to list Lambda function versions: %v", err)
		}

		for _, fv := range lo.Versions {
			if !strings.HasPrefix(*fv.Version, "$") {
				versions = append(versions, *fv.Version)
			}
		}

		if len(versions) > numToKeep {
			for _, fv := range versions[:len(versions)-numToKeep] {
				if !dryRun {
					_, err := cli.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
						FunctionName: aws.String(functionName),
						Qualifier:    aws.String(fv),
					})
					if ctx.Err() == context.Canceled {
						return
					}
					if err != nil {
						log.Fatalf("failed to delete Lambda function: %v", err)
					}
				}

				fmt.Printf("[DELETE] %s:%s\n", functionName, fv)
			}

			versions = versions[len(versions)-numToKeep:]
		}

		if lo.NextMarker == nil {
			break
		}
		marker = lo.NextMarker
	}

	for _, fv := range versions {
		fmt.Printf("[KEEP]   %s:%s\n", functionName, fv)
	}
}
