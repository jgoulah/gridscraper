package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var listService string

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List stored usage data",
	Long:  `Displays all stored electrical usage data from the database.`,
	RunE:  runList,
}

func init() {
	listCmd.Flags().StringVar(&listService, "service", "", "Filter by service (nyseg or coned)")
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	// Open database
	db, err := openDB()
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Determine which services to query
	services := []string{}
	if listService != "" {
		services = append(services, listService)
	} else {
		services = append(services, "nyseg", "coned")
	}

	// Query and display data for each service
	for _, service := range services {
		data, err := db.ListUsage(service)
		if err != nil {
			return fmt.Errorf("listing data for %s: %w", service, err)
		}

		if len(data) == 0 {
			if listService != "" || service == services[len(services)-1] {
				fmt.Printf("No data found for %s\n", service)
			}
			continue
		}

		fmt.Printf("\n%s Usage Data:\n", service)
		fmt.Println("----------------------------------------")
		fmt.Printf("%-12s  %10s\n", "Date", "kWh")
		fmt.Println("----------------------------------------")

		var total float64
		for _, record := range data {
			fmt.Printf("%-12s  %10.2f\n", record.Date.Format("2006-01-02"), record.KWh)
			total += record.KWh
		}

		fmt.Println("----------------------------------------")
		fmt.Printf("Total: %.2f kWh (%d records)\n", total, len(data))
	}

	return nil
}
