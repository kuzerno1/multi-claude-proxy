package cmd

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/kuzerno1/multi-claude-proxy/internal/account"
	"github.com/kuzerno1/multi-claude-proxy/internal/auth"
	"github.com/kuzerno1/multi-claude-proxy/internal/provider/copilot"
	"github.com/kuzerno1/multi-claude-proxy/internal/provider/zai"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
)

// accountsCmd represents the accounts command
var accountsCmd = &cobra.Command{
	Use:   "accounts",
	Short: "Manage accounts for providers",
	Long: `Manage the pool of accounts used by providers (Antigravity, Z.AI, and Copilot).

Antigravity accounts use OAuth authentication with Google Cloud Code API.
Z.AI accounts use API keys.
Copilot accounts use GitHub Device OAuth authentication.

Multiple accounts enable load balancing and failover when rate limits are hit.`,
}

var accountsAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new account",
	Long: `Add a new account to the pool.

If no --provider flag is specified, you will be prompted to select one.

Providers:
  antigravity - Google Cloud Code API (requires OAuth authentication)
  zai         - Z.AI API (requires API key, entered interactively)
  copilot     - GitHub Copilot (requires GitHub OAuth authentication)

Examples:
  multi-claude-proxy accounts add                        # Interactive provider selection
  multi-claude-proxy accounts add --provider antigravity # Add Antigravity account (OAuth)
  multi-claude-proxy accounts add --provider zai         # Add Z.AI account (prompts for key)
  multi-claude-proxy accounts add --provider copilot     # Add Copilot account (GitHub OAuth)`,
	RunE: runAccountsAdd,
}

var accountsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured accounts",
	RunE:  runAccountsList,
}

var accountsRemoveCmd = &cobra.Command{
	Use:   "remove [email]",
	Short: "Remove an account",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runAccountsRemove,
}

var accountsVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify account tokens are valid",
	RunE:  runAccountsVerify,
}

var (
	providerArg string
)

func init() {
	rootCmd.AddCommand(accountsCmd)

	accountsCmd.AddCommand(accountsAddCmd)
	accountsCmd.AddCommand(accountsListCmd)
	accountsCmd.AddCommand(accountsRemoveCmd)
	accountsCmd.AddCommand(accountsVerifyCmd)

	accountsAddCmd.Flags().StringVar(&providerArg, "provider", "", "Provider type (antigravity or zai)")
}

func runAccountsAdd(cmd *cobra.Command, args []string) error {
	// Normalize provider name
	provider := strings.ToLower(providerArg)

	// If no provider specified, show interactive selection
	if provider == "" {
		var err error
		provider, err = selectProvider()
		if err != nil {
			if err.Error() == "cancelled" {
				fmt.Println("Account addition cancelled.")
				return nil
			}
			return err
		}
		utils.Info("Selected provider: %s", provider)
	}

	if provider != "antigravity" && provider != "zai" && provider != "copilot" {
		return fmt.Errorf("invalid provider: %s (must be 'antigravity', 'zai', or 'copilot')", provider)
	}

	utils.Info("Adding new %s account...", provider)

	if provider == "zai" {
		return addZAIAccount()
	}

	if provider == "copilot" {
		return addCopilotAccount()
	}

	return addAntigravityAccount()
}

func addZAIAccount() error {
	fmt.Print("Enter Z.AI API key: ")
	var apiKey string
	// Use terminal password input to hide the key as user types.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		keyBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println() // Print newline after hidden input
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}
		apiKey = strings.TrimSpace(string(keyBytes))
	} else {
		// Fallback for non-terminal input (e.g., piped).
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}
		apiKey = strings.TrimSpace(input)
	}

	if apiKey == "" {
		return fmt.Errorf("API key is required for Z.AI provider")
	}

	// Verify the API key
	utils.Info("Verifying API key...")
	client := zai.NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.VerifyAPIKey(ctx, apiKey); err != nil {
		return fmt.Errorf("API key verification failed: %w", err)
	}

	// Generate a unique email-like identifier
	hash := sha256.Sum256([]byte(apiKey))
	shortHash := hex.EncodeToString(hash[:4])
	email := fmt.Sprintf("zai-%s", shortHash)

	// Add account to manager
	manager := account.NewManager("")
	if err := manager.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize account manager: %w", err)
	}

	newAccount := account.Account{
		Email:    email,
		Source:   "manual",
		Provider: "zai",
		APIKey:   apiKey,
	}

	if err := manager.AddAccount(newAccount); err != nil {
		return fmt.Errorf("failed to add account: %w", err)
	}

	utils.Success("Successfully added Z.AI account: %s", email)
	return nil
}

func addAntigravityAccount() error {

	// Generate authorization URL
	authURL, pkce, err := auth.GetAuthorizationURL()
	if err != nil {
		return fmt.Errorf("failed to generate authorization URL: %w", err)
	}

	// Always use manual code entry (works in containers, SSH, headless servers)
	utils.Info("OAuth flow: manual code input")
	fmt.Println()
	fmt.Println("Please visit the following URL to authorize:")
	fmt.Println()
	fmt.Println("  " + authURL)
	fmt.Println()
	fmt.Print("Paste the callback URL or authorization code here: ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	code, _, err := auth.ExtractCodeFromInput(strings.TrimSpace(input))
	if err != nil {
		return fmt.Errorf("failed to extract code: %w", err)
	}

	// Complete OAuth flow
	utils.Info("Exchanging code for tokens...")
	result, err := auth.CompleteOAuthFlow(code, pkce.Verifier)
	if err != nil {
		return fmt.Errorf("OAuth flow failed: %w", err)
	}

	// Add account to manager
	manager := account.NewManager("")
	if err := manager.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize account manager: %w", err)
	}

	newAccount := account.Account{
		Email:        result.Email,
		Source:       "oauth",
		Provider:     "antigravity",
		RefreshToken: result.RefreshToken,
		ProjectID:    result.ProjectID,
	}

	if err := manager.AddAccount(newAccount); err != nil {
		return fmt.Errorf("failed to add account: %w", err)
	}

	utils.Success("Successfully added account: %s", result.Email)
	if result.ProjectID != "" {
		utils.Info("Project ID: %s", result.ProjectID)
	}

	return nil
}

func addCopilotAccount() error {
	// Select account type
	accountType, err := selectCopilotAccountType()
	if err != nil {
		if err.Error() == "cancelled" {
			fmt.Println("Account addition cancelled.")
			return nil
		}
		return err
	}

	utils.Info("Using account type: %s", accountType)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Perform GitHub OAuth flow
	githubToken, err := performGitHubDeviceOAuth(ctx)
	if err != nil {
		return err
	}

	// Verify and get user info
	user, err := verifyCopilotAccess(ctx, githubToken, accountType)
	if err != nil {
		return err
	}

	// Save the account
	return saveCopilotAccount(githubToken, user, accountType)
}

// performGitHubDeviceOAuth initiates and completes the GitHub Device OAuth flow.
func performGitHubDeviceOAuth(ctx context.Context) (string, error) {
	utils.Info("Initiating GitHub Device OAuth flow...")
	deviceCode, err := copilot.GetDeviceCode(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get device code: %w", err)
	}

	fmt.Println()
	fmt.Println("Please visit the following URL to authorize:")
	fmt.Println()
	fmt.Printf("  %s\n", deviceCode.VerificationURI)
	fmt.Println()
	fmt.Printf("Enter this code: %s\n", deviceCode.UserCode)
	fmt.Println()
	fmt.Println("Waiting for authorization...")

	githubToken, err := copilot.PollAccessToken(ctx, deviceCode)
	if err != nil {
		return "", fmt.Errorf("authorization failed: %w", err)
	}

	utils.Success("GitHub authorization successful!")
	return githubToken, nil
}

// verifyCopilotAccess verifies the user has Copilot access and returns user info.
func verifyCopilotAccess(ctx context.Context, githubToken, accountType string) (*copilot.GitHubUser, error) {
	utils.Info("Fetching GitHub user info...")
	user, err := copilot.GetGitHubUser(ctx, githubToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}

	utils.Info("Verifying Copilot access...")
	_, err = copilot.GetCopilotToken(ctx, githubToken, copilot.AccountType(accountType))
	if err != nil {
		return nil, fmt.Errorf("Copilot verification failed: %w", err)
	}

	utils.Success("Copilot access verified!")
	return user, nil
}

// saveCopilotAccount saves the Copilot account to storage.
func saveCopilotAccount(githubToken string, user *copilot.GitHubUser, accountType string) error {
	manager := account.NewManager("")
	if err := manager.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize account manager: %w", err)
	}

	email := user.Login
	if email == "" {
		hash := sha256.Sum256([]byte(githubToken))
		shortHash := hex.EncodeToString(hash[:4])
		email = fmt.Sprintf("copilot-%s", shortHash)
	}

	newAccount := account.Account{
		Email:        email,
		Source:       "oauth",
		Provider:     "copilot",
		RefreshToken: githubToken,
		AccountType:  accountType,
	}

	if err := manager.AddAccount(newAccount); err != nil {
		return fmt.Errorf("failed to add account: %w", err)
	}

	utils.Success("Successfully added Copilot account: %s", email)
	return nil
}

func selectCopilotAccountType() (string, error) {
	accountTypes := []struct {
		name        string
		description string
	}{
		{"individual", "Personal GitHub Copilot subscription"},
		{"business", "GitHub Copilot Business (organization)"},
		{"enterprise", "GitHub Copilot Enterprise"},
	}

	fmt.Println("Select your Copilot account type:")
	fmt.Println()

	for i, t := range accountTypes {
		fmt.Printf("  %d. %s - %s\n", i+1, t.name, t.description)
	}

	fmt.Println()
	fmt.Print("Enter account type number (or 'q' to cancel): ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	if input == "q" || input == "" {
		return "", fmt.Errorf("cancelled")
	}

	var num int
	if _, err := fmt.Sscanf(input, "%d", &num); err != nil || num < 1 || num > len(accountTypes) {
		return "", fmt.Errorf("invalid selection: %s (must be 1-%d)", input, len(accountTypes))
	}

	return accountTypes[num-1].name, nil
}

func runAccountsList(cmd *cobra.Command, args []string) error {
	manager := account.NewManager("")
	if err := manager.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize account manager: %w", err)
	}

	accounts := manager.GetAllAccounts()
	if len(accounts) == 0 {
		fmt.Println("No accounts configured.")
		fmt.Println()
		fmt.Println("To add an account, run:")
		fmt.Println("  multi-claude-proxy accounts add")
		return nil
	}

	fmt.Printf("Configured accounts (%d):\n\n", len(accounts))

	for i, acc := range accounts {
		status := "OK"
		statusColor := "\033[32m" // green

		if acc.IsInvalid {
			status = "INVALID"
			statusColor = "\033[31m" // red
		}

		// Check for rate limits
		now := time.Now().UnixMilli()
		for modelID, limit := range acc.ModelRateLimits {
			if limit.IsRateLimited && limit.ResetTime > now {
				waitMs := limit.ResetTime - now
				status = fmt.Sprintf("RATE-LIMITED (%s, %s)",
					modelID, utils.FormatDuration(time.Duration(waitMs)*time.Millisecond))
				statusColor = "\033[33m" // yellow
				break
			}
		}

		fmt.Printf("  %d. %s\n", i+1, acc.Email)
		fmt.Printf("     Provider: %s\n", acc.Provider)
		fmt.Printf("     Source: %s\n", acc.Source)
		fmt.Printf("     Status: %s%s\033[0m\n", statusColor, status)
		if acc.IsInvalid && acc.InvalidReason != "" {
			fmt.Printf("     Reason: %s\n", acc.InvalidReason)
		}
		if acc.ProjectID != "" {
			fmt.Printf("     Project: %s\n", acc.ProjectID)
		}
		if acc.LastUsed != nil {
			fmt.Printf("     Last used: %s\n", acc.LastUsed.Format(time.RFC3339))
		}
		fmt.Println()
	}

	return nil
}

func runAccountsRemove(cmd *cobra.Command, args []string) error {
	manager := account.NewManager("")
	if err := manager.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize account manager: %w", err)
	}

	accounts := manager.GetAllAccounts()
	if len(accounts) == 0 {
		fmt.Println("No accounts to remove.")
		return nil
	}

	var email string

	if len(args) > 0 {
		email = args[0]
	} else {
		// Interactive selection
		fmt.Println("Select an account to remove:")
		fmt.Println()

		for i, acc := range accounts {
			fmt.Printf("  %d. %s (%s, %s)\n", i+1, acc.Email, acc.Provider, acc.Source)
		}

		fmt.Println()
		fmt.Print("Enter account number (or 'q' to cancel): ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		if input == "q" || input == "" {
			fmt.Println("Cancelled.")
			return nil
		}

		var num int
		if _, err := fmt.Sscanf(input, "%d", &num); err != nil || num < 1 || num > len(accounts) {
			return fmt.Errorf("invalid selection: %s", input)
		}

		email = accounts[num-1].Email
	}

	if err := manager.RemoveAccount(email); err != nil {
		return fmt.Errorf("failed to remove account: %w", err)
	}

	utils.Success("Removed account: %s", email)
	return nil
}

func runAccountsVerify(cmd *cobra.Command, args []string) error {
	manager := account.NewManager("")
	if err := manager.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize account manager: %w", err)
	}

	accounts := manager.GetAllAccounts()
	if len(accounts) == 0 {
		fmt.Println("No accounts to verify.")
		return nil
	}

	utils.Info("Verifying %d account(s)...", len(accounts))
	fmt.Println()

	allValid := true

	for i, acc := range accounts {
		fmt.Printf("  %d. %s (%s)... ", i+1, acc.Email, acc.Provider)

		if acc.Provider == "zai" {
			// Verify Z.AI account by calling models endpoint
			if acc.APIKey == "" {
				fmt.Printf("\033[31mFAILED\033[0m\n")
				fmt.Printf("     Error: no API key\n")
				allValid = false
				continue
			}

			client := zai.NewClient()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := client.VerifyAPIKey(ctx, acc.APIKey)
			cancel()

			if err != nil {
				fmt.Printf("\033[31mFAILED\033[0m\n")
				fmt.Printf("     Error: %v\n", err)
				allValid = false
				continue
			}

			fmt.Printf("\033[32mOK\033[0m\n")
			continue
		}

		if acc.Provider == "copilot" {
			// Verify Copilot account by getting a Copilot token
			if acc.RefreshToken == "" {
				fmt.Printf("\033[31mFAILED\033[0m\n")
				fmt.Printf("     Error: no GitHub token\n")
				allValid = false
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err := copilot.GetCopilotToken(ctx, acc.RefreshToken, copilot.AccountType(acc.AccountType))
			cancel()

			if err != nil {
				fmt.Printf("\033[31mFAILED\033[0m\n")
				fmt.Printf("     Error: %v\n", err)
				allValid = false
				continue
			}

			fmt.Printf("\033[32mOK\033[0m\n")
			continue
		}

		// Antigravity account verification
		token, err := manager.GetTokenForAccount(&acc)
		if err != nil {
			fmt.Printf("\033[31mFAILED\033[0m\n")
			fmt.Printf("     Error: %v\n", err)
			allValid = false
			continue
		}

		// Try to get user email to verify token works
		email, err := auth.GetUserEmail(token)
		if err != nil {
			fmt.Printf("\033[31mFAILED\033[0m\n")
			fmt.Printf("     Error: %v\n", err)
			allValid = false
			continue
		}

		fmt.Printf("\033[32mOK\033[0m")
		if email != acc.Email {
			fmt.Printf(" (email mismatch: %s)", email)
		}
		fmt.Println()
	}

	fmt.Println()
	if allValid {
		utils.Success("All accounts verified successfully!")
	} else {
		utils.Warn("Some accounts failed verification. Run 'accounts add' to re-authenticate.")
	}

	return nil
}

// selectProvider shows an interactive menu to select a provider.
func selectProvider() (string, error) {
	providers := []struct {
		name        string
		description string
	}{
		{"antigravity", "Google Cloud Code (OAuth authentication)"},
		{"zai", "Z.AI API (API key authentication)"},
		{"copilot", "GitHub Copilot (GitHub OAuth authentication)"},
	}

	fmt.Println("Select a provider to add:")
	fmt.Println()

	for i, p := range providers {
		fmt.Printf("  %d. %s - %s\n", i+1, p.name, p.description)
	}

	fmt.Println()
	fmt.Print("Enter provider number (or 'q' to cancel): ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	if input == "q" || input == "" {
		return "", fmt.Errorf("cancelled")
	}

	var num int
	if _, err := fmt.Sscanf(input, "%d", &num); err != nil || num < 1 || num > len(providers) {
		return "", fmt.Errorf("invalid selection: %s (must be 1-%d)", input, len(providers))
	}

	return providers[num-1].name, nil
}
