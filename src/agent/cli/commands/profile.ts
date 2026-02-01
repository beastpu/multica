/**
 * Profile command - Manage agent profiles
 *
 * Usage:
 *   multica profile list              List all profiles
 *   multica profile new <id>          Create a new profile
 *   multica profile show <id>         Show profile contents
 *   multica profile edit <id>         Open profile in file manager
 *   multica profile delete <id>       Delete a profile
 */

import { existsSync, readdirSync, rmSync, readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import * as readline from "node:readline";
import {
  createAgentProfile,
  loadAgentProfile,
  getProfileDir,
  profileExists,
} from "../../profile/index.js";
import { DATA_DIR } from "../../../shared/index.js";
import { cyan, yellow, green, dim, red, brightCyan, gray, colors } from "../colors.js";
import { Agent } from "../../runner.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const SETUP_SKILL_PATH = join(__dirname, "../../../../skills/profile-setup/SKILL.md");

const PROFILES_DIR = join(DATA_DIR, "agent-profiles");

type Command = "new" | "list" | "show" | "edit" | "delete" | "setup" | "help";

function printHelp() {
  console.log(`
${cyan("Usage:")} multica profile <command> [options]

${cyan("Commands:")}
  ${yellow("list")}                List all profiles
  ${yellow("new")} <id>            Create a new profile
  ${yellow("setup")} <id>          Interactive setup for a profile
  ${yellow("show")} <id>           Show profile contents
  ${yellow("edit")} <id>           Open profile directory in file manager
  ${yellow("delete")} <id>         Delete a profile
  ${yellow("help")}                Show this help

${cyan("Profile Structure:")}
  Each profile is a directory containing:
  - soul.md       Agent identity, personality and behavior
  - user.md       Information about the user
  - workspace.md  Workspace rules and conventions
  - memory.md     Persistent knowledge

${cyan("Examples:")}
  ${dim("# Create a new profile")}
  multica profile new my-agent

  ${dim("# Interactive setup")}
  multica profile setup my-agent

  ${dim("# List all profiles")}
  multica profile list

  ${dim("# Use a profile")}
  multica chat --profile my-agent
`);
}

function cmdNew(profileId: string | undefined) {
  if (!profileId) {
    console.error("Error: Profile ID is required");
    console.error("Usage: multica profile new <id>");
    process.exit(1);
  }

  // Validate profile ID
  if (!/^[a-zA-Z0-9_-]+$/.test(profileId)) {
    console.error("Error: Profile ID can only contain letters, numbers, hyphens, and underscores");
    process.exit(1);
  }

  if (profileExists(profileId)) {
    console.error(`Error: Profile "${profileId}" already exists`);
    console.error(`Location: ${getProfileDir(profileId)}`);
    process.exit(1);
  }

  const profile = createAgentProfile(profileId);
  const dir = getProfileDir(profileId);

  console.log(`${green("Created profile:")} ${yellow(profile.id)}`);
  console.log(`${dim("Location:")} ${dir}`);
  console.log("");
  console.log("Files created:");
  console.log("  - soul.md       (identity, personality and behavior)");
  console.log("  - user.md       (information about the user)");
  console.log("  - workspace.md  (workspace rules and conventions)");
  console.log("  - memory.md     (persistent knowledge)");
  console.log("");
  console.log("Run interactive setup to personalize your agent:");
  console.log(`  multica profile setup ${profileId}`);
  console.log("");
  console.log("Or start chatting directly:");
  console.log(`  multica chat --profile ${profileId}`);
}

function cmdList() {
  if (!existsSync(PROFILES_DIR)) {
    console.log("No profiles found.");
    console.log(`Create one with: multica profile new <id>`);
    return;
  }

  const entries = readdirSync(PROFILES_DIR, { withFileTypes: true });
  const profiles = entries.filter((e) => e.isDirectory()).map((e) => e.name);

  if (profiles.length === 0) {
    console.log("No profiles found.");
    console.log(`Create one with: multica profile new <id>`);
    return;
  }

  console.log(`\n${cyan("Available profiles:")}\n`);
  for (const id of profiles) {
    const dir = getProfileDir(id);
    console.log(`  ${yellow(id)}`);
    console.log(`    ${dim(dir)}`);
  }
  console.log("");
  console.log(`${dim(`Total: ${profiles.length} profile(s)`)}`);
}

function cmdShow(profileId: string | undefined) {
  if (!profileId) {
    console.error("Error: Profile ID is required");
    console.error("Usage: multica profile show <id>");
    process.exit(1);
  }

  const profile = loadAgentProfile(profileId);
  if (!profile) {
    console.error(`Error: Profile "${profileId}" not found`);
    console.error(`Create it with: multica profile new ${profileId}`);
    process.exit(1);
  }

  console.log(`\n${cyan("Profile:")} ${yellow(profile.id)}`);
  console.log(`${dim("Location:")} ${getProfileDir(profileId)}`);
  console.log("");

  if (profile.soul) {
    console.log(`${green("=== soul.md ===")}`);
    console.log(profile.soul.trim());
    console.log("");
  }

  if (profile.user) {
    console.log(`${green("=== user.md ===")}`);
    console.log(profile.user.trim());
    console.log("");
  }

  if (profile.workspace) {
    console.log(`${green("=== workspace.md ===")}`);
    console.log(profile.workspace.trim());
    console.log("");
  }

  if (profile.memory) {
    console.log(`${green("=== memory.md ===")}`);
    console.log(profile.memory.trim());
    console.log("");
  }
}

async function cmdEdit(profileId: string | undefined) {
  if (!profileId) {
    console.error("Error: Profile ID is required");
    console.error("Usage: multica profile edit <id>");
    process.exit(1);
  }

  if (!profileExists(profileId)) {
    console.error(`Error: Profile "${profileId}" not found`);
    console.error(`Create it with: multica profile new ${profileId}`);
    process.exit(1);
  }

  const dir = getProfileDir(profileId);
  const { spawn } = await import("node:child_process");

  // Open in default file manager
  const cmd = process.platform === "darwin" ? "open" : process.platform === "win32" ? "explorer" : "xdg-open";
  spawn(cmd, [dir], { detached: true, stdio: "ignore" }).unref();

  console.log(`${green("Opened:")} ${dir}`);
}

function cmdDelete(profileId: string | undefined) {
  if (!profileId) {
    console.error("Error: Profile ID is required");
    console.error("Usage: multica profile delete <id>");
    process.exit(1);
  }

  if (!profileExists(profileId)) {
    console.error(`Error: Profile "${profileId}" not found`);
    process.exit(1);
  }

  const dir = getProfileDir(profileId);

  try {
    rmSync(dir, { recursive: true });
    console.log(`${green("Deleted:")} ${profileId}`);
  } catch (err) {
    console.error(`${red("Error:")} Failed to delete profile: ${err instanceof Error ? err.message : String(err)}`);
    process.exit(1);
  }
}

/**
 * Load setup skill instructions from SKILL.md
 */
function loadSetupSkillInstructions(): string | undefined {
  try {
    if (!existsSync(SETUP_SKILL_PATH)) {
      return undefined;
    }
    const content = readFileSync(SETUP_SKILL_PATH, "utf-8");
    // Extract instructions after frontmatter (after the second ---)
    const parts = content.split("---");
    if (parts.length >= 3) {
      return parts.slice(2).join("---").trim();
    }
    return content;
  } catch {
    return undefined;
  }
}

/**
 * Interactive setup for a profile
 */
async function cmdSetup(profileId: string | undefined) {
  if (!profileId) {
    console.error("Error: Profile ID is required");
    console.error("Usage: multica profile setup <id>");
    process.exit(1);
  }

  if (!profileExists(profileId)) {
    console.error(`Error: Profile "${profileId}" not found`);
    console.error(`Create it first with: multica profile new ${profileId}`);
    process.exit(1);
  }

  // Check TTY
  if (!process.stdin.isTTY) {
    console.error(colors.error("Error: Interactive setup requires a TTY."));
    process.exit(1);
  }

  // Load setup skill instructions
  const setupInstructions = loadSetupSkillInstructions();
  if (!setupInstructions) {
    console.error(colors.error("Error: Could not load setup skill instructions."));
    process.exit(1);
  }

  const profileDir = getProfileDir(profileId);

  // Build system prompt with setup instructions
  const systemPrompt = `${setupInstructions}

## Profile Context

You are setting up the profile "${profileId}".
The profile directory is: ${profileDir}

Available profile files to update:
- ${profileDir}/user.md - Information about the user
- ${profileDir}/workspace.md - Workspace rules and conventions
- ${profileDir}/config.json - Configuration (provider, model, etc.)

Start the setup conversation now.`;

  // Create agent with setup instructions
  const agent = new Agent({
    profileId,
    systemPrompt,
  });

  // Print welcome
  console.log("");
  console.log(cyan("╭─────────────────────────────────────────╮"));
  console.log(`${cyan("│")}     ${brightCyan("Profile Setup Wizard")}               ${cyan("│")}`);
  console.log(cyan("╰─────────────────────────────────────────╯"));
  console.log("");
  console.log(`${dim("Profile:")} ${yellow(profileId)}`);
  console.log(`${dim("Location:")} ${gray(profileDir)}`);
  console.log(`${dim("Type")} ${cyan("/exit")} ${dim("to finish setup.")}`);
  console.log("");

  // Start the conversation with an initial prompt
  try {
    await agent.run("Start the setup process.");
    console.log("");
  } catch (err) {
    console.error(`\n${colors.error(`Error: ${err instanceof Error ? err.message : String(err)}`)}`);
  }

  // Interactive loop
  const rl = readline.createInterface({
    input: process.stdin,
    output: process.stdout,
  });

  const askQuestion = (prompt: string): Promise<string> => {
    return new Promise((resolve) => {
      rl.question(prompt, (answer) => {
        resolve(answer);
      });
    });
  };

  let running = true;

  // Handle Ctrl+C
  process.on("SIGINT", () => {
    console.log(`\n${dim("Setup cancelled.")}`);
    rl.close();
    process.exit(0);
  });

  while (running) {
    const input = await askQuestion(`${brightCyan("You:")} `);
    const trimmed = input.trim();

    if (!trimmed) continue;

    // Check for exit command
    if (trimmed.toLowerCase() === "/exit" || trimmed.toLowerCase() === "/quit" || trimmed.toLowerCase() === "/q") {
      console.log("");
      console.log(`${green("Setup complete!")} Your profile has been updated.`);
      console.log(`${dim("Start chatting with:")} multica chat --profile ${profileId}`);
      console.log("");
      running = false;
      break;
    }

    // Send to agent
    try {
      console.log("");
      await agent.run(trimmed);
      console.log("");
    } catch (err) {
      console.error(`\n${colors.error(`Error: ${err instanceof Error ? err.message : String(err)}`)}`);
      console.log("");
    }
  }

  rl.close();
}

export async function profileCommand(args: string[]): Promise<void> {
  const command = (args[0] || "help") as Command;
  const arg1 = args[1];

  if (args.includes("--help") || args.includes("-h")) {
    printHelp();
    return;
  }

  switch (command) {
    case "new":
      cmdNew(arg1);
      break;
    case "list":
      cmdList();
      break;
    case "show":
      cmdShow(arg1);
      break;
    case "edit":
      await cmdEdit(arg1);
      break;
    case "delete":
      cmdDelete(arg1);
      break;
    case "setup":
      await cmdSetup(arg1);
      break;
    case "help":
    default:
      printHelp();
      break;
  }
}
