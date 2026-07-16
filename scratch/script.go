package main

import (
	"fmt"
	"strings"
)

func formatAmount(val float64) string {
	if val >= 10000000 {
		return fmt.Sprintf("₹%g Crores", val/10000000)
	} else if val >= 100000 {
		return fmt.Sprintf("₹%g Lakhs", val/100000)
	} else if val > 0 {
		return fmt.Sprintf("₹%g", val)
	}
	return ""
}

func formatBudgetTitleSuffix(min, max float64) string {
	if min > 0 && max > 0 {
		return fmt.Sprintf(" Between %s to %s", formatAmount(min), formatAmount(max))
	} else if max > 0 {
		return fmt.Sprintf(" Under %s", formatAmount(max))
	} else if min > 0 {
		return fmt.Sprintf(" Above %s", formatAmount(min))
	}
	return ""
}

func buildDynamicListingTitle(baseTitle string, minInv, maxInv float64, location string) string {
	title := baseTitle
	title = strings.TrimSuffix(title, " in India")

	budgetSuffix := formatBudgetTitleSuffix(minInv, maxInv)
	if budgetSuffix != "" {
		title += budgetSuffix
	}

	if location != "" {
		title += " in " + location
	} else {
		title += " in India"
	}

	return title
}

func main() {
	fmt.Println(buildDynamicListingTitle("Franchise Opportunities in India", 0, 1000000, ""))
	fmt.Println(buildDynamicListingTitle("Food & Beverage Franchises", 1000000, 1500000, "Delhi NCR"))
	fmt.Println(buildDynamicListingTitle("Healthcare and Technology Associations", 0, 0, "Mumbai"))
}
