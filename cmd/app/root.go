package app

import (
	"fmt"
	"github.com/spf13/cobra"
	"os"
)

var rootCmd = &cobra.Command{
	Use:   "cri-shim",
	Short: "cri shim for kubelet",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if rootCmd.SilenceErrors {
			fmt.Println(err)
		}
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(newServerCmd())
	rootCmd.AddCommand(newVersionCmd())
}
