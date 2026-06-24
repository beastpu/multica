#!/usr/bin/env node
// MVP tooling for the private Defect Fix LLM Wiki.
//
// Commands:
//   pnpm defect-fix-wiki init
//   pnpm defect-fix-wiki scan --repo multica-ai/multica --limit 50
//   pnpm defect-fix-wiki index
//   pnpm defect-fix-wiki lint
//   pnpm defect-fix-wiki query --text "runtime timeout" --module server
//   pnpm defect-fix-wiki trial --issue MUL-123 --result hit --query "..."
//   pnpm defect-fix-wiki status

import { execFileSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  writeFileSync,
} from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const defaultWikiDir = process.env.DEFECT_FIX_WIKI_DIR || "defect-fix-wiki";
const defaultRepo = process.env.DEFECT_FIX_WIKI_REPO || "multica-ai/multica";
const wikiKinds = ["patterns", "antipatterns", "concepts", "playbooks", "synthesis"];
const caseRequiredFields = [
  "id",
  "kind",
  "title",
  "status",
  "confidence",
  "visibility",
  "repo",
  "source_pr",
  "source_pr_number",
  "source_issue_refs",
  "merge_commit",
  "modules",
  "signals",
  "code_refs",
  "fix_pattern",
  "verification",
  "gotchas",
  "tags",
];

main();

function main() {
  const [command, ...rest] = process.argv.slice(2);
  const args = parseArgs(rest);
  const wikiRoot = resolve(repoRoot, args["wiki-dir"] || defaultWikiDir);

  try {
    switch (command) {
      case "init":
        ensureWikiTree(wikiRoot);
        console.log(`Initialized ${relative(repoRoot, wikiRoot)}`);
        return;
      case "scan":
        ensureWikiTree(wikiRoot);
        scanClosedPrs(wikiRoot, args);
        return;
      case "index":
        ensureWikiTree(wikiRoot);
        writeIndexes(wikiRoot);
        return;
      case "lint":
        ensureWikiTree(wikiRoot);
        lintWiki(wikiRoot);
        return;
      case "query":
        queryWiki(wikiRoot, args);
        return;
      case "trial":
        ensureWikiTree(wikiRoot);
        recordTrial(wikiRoot, args);
        return;
      case "status":
        ensureWikiTree(wikiRoot);
        printStatus(wikiRoot, args);
        return;
      case "help":
      case undefined:
        printHelp();
        return;
      default:
        throw new Error(`Unknown command: ${command}`);
    }
  } catch (err) {
    console.error(err instanceof Error ? err.message : String(err));
    process.exit(1);
  }
}

function printHelp() {
  console.log(`Usage: pnpm defect-fix-wiki <command> [flags]

Commands:
  init                         Create the private wiki directory structure
  scan --limit 50              Scan closed GitHub PRs into source records and candidate drafts
  index                        Regenerate cases.index.jsonl and tags.index.json
  lint                         Validate official wiki cases and generated indexes
  query --text "..."           Search cases.index.jsonl for internal agent use
  trial --issue MUL-123        Record one query-before-fix pilot result
  status                       Show MVP completion evidence and remaining gaps

Common flags:
  --wiki-dir defect-fix-wiki   Override wiki directory
  --repo multica-ai/multica    GitHub repository for scan
  --limit 50                   Scan/query limit
  --json                       Print machine-readable output where supported
`);
}

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    if (!token.startsWith("--")) {
      if (!out._) out._ = [];
      out._.push(token);
      continue;
    }
    const eq = token.indexOf("=");
    if (eq !== -1) {
      out[token.slice(2, eq)] = token.slice(eq + 1);
      continue;
    }
    const key = token.slice(2);
    const next = argv[i + 1];
    if (next == null || next.startsWith("--")) {
      out[key] = true;
    } else {
      out[key] = next;
      i += 1;
    }
  }
  return out;
}

function ensureWikiTree(root) {
  const dirs = [
    "",
    "schema",
    "sources/prs",
    "sources/issues",
    "candidates",
    "indexes",
    "reports",
    "trials",
    ...wikiKinds.map((kind) => `wiki/${kind}`),
  ];
  for (const dir of dirs) {
    mkdirSync(join(root, dir), { recursive: true });
  }
  for (const dir of ["sources/prs", "sources/issues", "candidates", "indexes", "reports", "trials", ...wikiKinds.map((kind) => `wiki/${kind}`)]) {
    const keep = join(root, dir, ".gitkeep");
    if (!existsSync(keep)) writeFileSync(keep, "");
  }
}

function scanClosedPrs(wikiRoot, args) {
  const ghRepo = String(args.repo || defaultRepo);
  const limit = Number.parseInt(String(args.limit || "50"), 10);
  if (!Number.isFinite(limit) || limit <= 0) {
    throw new Error(`--limit must be a positive integer`);
  }

  const fields = [
    "number",
    "title",
    "url",
    "state",
    "mergedAt",
    "closedAt",
    "baseRefName",
    "headRefName",
    "author",
    "body",
    "files",
    "closingIssuesReferences",
    "mergeCommit",
  ].join(",");
  const prs = ghJson(["pr", "list", "-R", ghRepo, "--state", "closed", "--limit", String(limit), "--json", fields]);

  const report = {
    repo: ghRepo,
    scanned: prs.length,
    source_records_written: 0,
    candidates_written: 0,
    candidate_pr_numbers: [],
    skipped: [],
  };

  for (const pr of prs) {
    const source = buildSourceRecord(ghRepo, pr);
    const sourcePath = join(wikiRoot, "sources/prs", `pr-${pr.number}.yaml`);
    writeText(sourcePath, stringifyYaml(source));
    report.source_records_written += 1;

    if (!source.classification.candidate) {
      report.skipped.push({ pr_number: pr.number, reasons: source.classification.reasons });
      continue;
    }

    const candidate = buildCandidateDraft(ghRepo, pr, source);
    const candidatePath = join(wikiRoot, "candidates", `pr-${pr.number}.candidate.md`);
    writeText(candidatePath, candidate);
    report.candidates_written += 1;
    report.candidate_pr_numbers.push(pr.number);
  }

  const reportPath = join(wikiRoot, "reports", "ingestion-report.json");
  writeText(reportPath, JSON.stringify(report, null, 2) + "\n");
  console.log(`Scanned ${report.scanned} closed PRs from ${ghRepo}`);
  console.log(`Wrote ${report.source_records_written} source records`);
  console.log(`Wrote ${report.candidates_written} candidate drafts`);
}

function buildSourceRecord(ghRepo, pr) {
  const files = Array.isArray(pr.files) ? pr.files : [];
  const linkedIssues = collectLinkedIssues(pr);
  const testFiles = files.filter((file) => isTestFile(file.path));
  const docsOnly = files.length > 0 && files.every((file) => isDocsOnlyPath(file.path));
  const merged = Boolean(pr.mergedAt);
  const candidate = merged && linkedIssues.length > 0 && testFiles.length > 0 && !docsOnly;
  const reasons = [];
  if (merged) reasons.push("merged");
  else reasons.push("not merged");
  if (linkedIssues.length > 0) reasons.push("linked issue detected");
  else reasons.push("missing linked issue");
  if (testFiles.length > 0) reasons.push("test file changed");
  else reasons.push("missing test diff");
  if (docsOnly) reasons.push("docs-only diff");

  return {
    id: `source.github.pr.${pr.number}`,
    repo: ghRepo,
    pr_number: pr.number,
    url: pr.url,
    state: pr.mergedAt ? "merged" : String(pr.state || "closed").toLowerCase(),
    title: pr.title || "",
    author: pr.author?.login || "",
    base_ref: pr.baseRefName || "",
    head_ref: pr.headRefName || "",
    merged_at: pr.mergedAt || "",
    closed_at: pr.closedAt || "",
    merge_commit: pr.mergeCommit?.oid || "",
    linked_issues: linkedIssues,
    changed_files: files.map((file) => ({
      path: file.path,
      additions: file.additions ?? 0,
      deletions: file.deletions ?? 0,
      change_type: file.changeType || "",
    })),
    test_files_changed: testFiles.map((file) => ({
      path: file.path,
    })),
    classification: {
      candidate,
      reasons,
    },
  };
}

function buildCandidateDraft(ghRepo, pr, source) {
  const mainFile = source.changed_files.find((file) => !isTestFile(file.path) && !isDocsOnlyPath(file.path)) || source.changed_files[0];
  const mergeCommit = source.merge_commit || "<missing-merge-commit>";
  const contentHash = mainFile?.path && source.merge_commit
    ? getGitHubBlobSha(ghRepo, mainFile.path, source.merge_commit)
    : "";
  const moduleNames = deriveModules(source.changed_files.map((file) => file.path));
  const tags = deriveTags(pr.title, moduleNames);
  const caseId = `pattern.${moduleNames[0] || "general"}.${slugify(pr.title || `pr-${pr.number}`)}`;
  const issueRefs = source.linked_issues.length > 0 ? source.linked_issues : ["needs_review"];
  const confidence = contentHash ? "medium" : "low";

  const frontmatter = {
    id: caseId,
    kind: "pattern",
    title: pr.title || `PR ${pr.number}`,
    status: "needs_review",
    confidence,
    visibility: "private",
    repo: ghRepo,
    source_pr: pr.url,
    source_pr_number: pr.number,
    source_issue_refs: issueRefs,
    merged_at: source.merged_at,
    merge_commit: mergeCommit,
    first_ingested_at: new Date().toISOString().slice(0, 10),
    last_reviewed_at: "",
    modules: moduleNames,
    signals: [pr.title || `PR ${pr.number}`],
    code_refs: [
      {
        repo: ghRepo,
        path: mainFile?.path || "needs_review",
        line_hint: 1,
        ref_kind: "root_cause",
        verified_commit: mergeCommit,
        content_hash: contentHash || "needs_review",
      },
    ],
    fix_pattern: "needs_review",
    verification: source.test_files_changed.map((file) => file.path),
    gotchas: ["needs_review"],
    related: [],
    tags,
  };

  return `---\n${stringifyYaml(frontmatter)}---\n\n# ${pr.title || `PR ${pr.number}`}\n\n> Status: candidate draft. A wiki-librarian must verify root cause, fix pattern, verification, and non-applicable details before moving this page into \`wiki/\`.\n\n## 症状\n\nneeds_review\n\nSource signal:\n\n${quoteBlock(pr.title || "")}\n\n## 根因\n\nneeds_review\n\n## 正确修复模式\n\nneeds_review\n\n## 为什么这个修法正确\n\nneeds_review\n\n## 验证方式\n\n${source.test_files_changed.length > 0 ? source.test_files_changed.map((file) => `- Test file changed: \`${file.path}\``).join("\n") : "- needs_review"}\n\n## 适用边界\n\nneeds_review\n\n## 不要照搬\n\nneeds_review\n\n## Source\n\n- PR: ${pr.url}\n- Linked issues: ${issueRefs.join(", ")}\n- Merge commit: ${mergeCommit}\n`;
}

function writeIndexes(wikiRoot) {
  const cases = loadCasePages(wikiRoot);
  const indexLines = [];
  const tags = {};

  for (const page of cases) {
    const fm = page.frontmatter;
    const row = {
      id: fm.id,
      kind: fm.kind,
      status: fm.status,
      confidence: fm.confidence,
      repo: fm.repo,
      modules: asArray(fm.modules),
      tags: asArray(fm.tags),
      signals: asArray(fm.signals),
      files: asArray(fm.code_refs).map((ref) => ref.path).filter(Boolean),
      source_pr_number: fm.source_pr_number,
      last_reviewed_at: fm.last_reviewed_at || "",
      path: relative(wikiRoot, page.path),
    };
    indexLines.push(JSON.stringify(row));
    for (const tag of row.tags) {
      if (!tags[tag]) tags[tag] = [];
      tags[tag].push(fm.id);
    }
  }

  indexLines.sort();
  const sortedTags = Object.fromEntries(
    Object.entries(tags)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([tag, ids]) => [tag, ids.sort()]),
  );
  writeText(join(wikiRoot, "indexes/cases.index.jsonl"), indexLines.join("\n") + (indexLines.length > 0 ? "\n" : ""));
  writeText(join(wikiRoot, "indexes/tags.index.json"), JSON.stringify(sortedTags, null, 2) + "\n");
  console.log(`Indexed ${cases.length} official cases`);
}

function lintWiki(wikiRoot) {
  const cases = loadCasePages(wikiRoot);
  const errors = [];
  const warnings = [];
  const ids = new Map();

  for (const page of cases) {
    const fm = page.frontmatter;
    const rel = relative(repoRoot, page.path);
    for (const field of caseRequiredFields) {
      if (fm[field] == null || fm[field] === "" || (Array.isArray(fm[field]) && fm[field].length === 0)) {
        errors.push(`${rel}: missing required field ${field}`);
      }
    }
    if (typeof fm.id === "string") {
      if (!/^[a-z]+(?:\.[a-z0-9_]+){2,}$/.test(fm.id)) {
        errors.push(`${rel}: id must be lower-case dot-separated with at least 3 segments`);
      }
      if (ids.has(fm.id)) {
        errors.push(`${rel}: duplicate id ${fm.id} also used by ${relative(repoRoot, ids.get(fm.id))}`);
      } else {
        ids.set(fm.id, page.path);
      }
    }
    if (fm.visibility !== "private") {
      errors.push(`${rel}: visibility must be private`);
    }
    if (fm.source_pr_number != null) {
      const sourcePath = join(wikiRoot, "sources/prs", `pr-${fm.source_pr_number}.yaml`);
      if (!existsSync(sourcePath)) {
        errors.push(`${rel}: missing source record ${relative(repoRoot, sourcePath)}`);
      }
    }
    for (const ref of asArray(fm.code_refs)) {
      if (!ref.path) errors.push(`${rel}: code_refs entry missing path`);
      if (!ref.content_hash) errors.push(`${rel}: code_refs entry missing content_hash`);
    }
  }

  for (const page of cases) {
    const rel = relative(repoRoot, page.path);
    for (const relatedId of asArray(page.frontmatter.related)) {
      if (!ids.has(relatedId)) {
        errors.push(`${rel}: related id does not exist: ${relatedId}`);
      }
    }
  }

  const secretHits = scanForSecrets(wikiRoot);
  for (const hit of secretHits) {
    errors.push(`${relative(repoRoot, hit.path)}: possible secret matched ${hit.label}`);
  }

  const expected = buildIndexSnapshot(wikiRoot, cases);
  const indexPath = join(wikiRoot, "indexes/cases.index.jsonl");
  const tagsPath = join(wikiRoot, "indexes/tags.index.json");
  if (!existsSync(indexPath)) {
    errors.push(`${relative(repoRoot, indexPath)} missing; run pnpm defect-fix-wiki index`);
  } else if (readFileSync(indexPath, "utf8") !== expected.casesIndex) {
    errors.push(`${relative(repoRoot, indexPath)} is stale; run pnpm defect-fix-wiki index`);
  }
  if (!existsSync(tagsPath)) {
    errors.push(`${relative(repoRoot, tagsPath)} missing; run pnpm defect-fix-wiki index`);
  } else if (readFileSync(tagsPath, "utf8") !== expected.tagsIndex) {
    errors.push(`${relative(repoRoot, tagsPath)} is stale; run pnpm defect-fix-wiki index`);
  }

  const report = {
    ok: errors.length === 0,
    cases: cases.length,
    errors,
    warnings,
  };
  writeText(join(wikiRoot, "reports/lint-report.json"), JSON.stringify(report, null, 2) + "\n");
  if (errors.length > 0) {
    console.error(`Defect-fix wiki lint failed with ${errors.length} error(s):`);
    for (const error of errors) console.error(`- ${error}`);
    process.exit(1);
  }
  console.log(`Defect-fix wiki lint passed (${cases.length} cases)`);
}

function queryWiki(wikiRoot, args) {
  const indexPath = join(wikiRoot, "indexes/cases.index.jsonl");
  if (!existsSync(indexPath)) {
    throw new Error(`${relative(repoRoot, indexPath)} missing; run pnpm defect-fix-wiki index`);
  }
  const text = String(args.text || args._?.join(" ") || "").trim();
  const modules = asArray(args.module || args.modules).flatMap((value) => String(value).split(",")).filter(Boolean);
  const files = asArray(args.file || args.files).flatMap((value) => String(value).split(",")).filter(Boolean);
  const limit = Number.parseInt(String(args.limit || "5"), 10);
  const rows = readFileSync(indexPath, "utf8")
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line));
  const scored = rows
    .map((row) => ({ row, score: scoreCase(row, { text, modules, files }) }))
    .filter((item) => item.score > 0 || (!text && modules.length === 0 && files.length === 0))
    .sort((a, b) => b.score - a.score || String(a.row.id).localeCompare(String(b.row.id)))
    .slice(0, limit);

  if (args.json) {
    console.log(JSON.stringify(scored.map((item) => ({ score: item.score, ...item.row })), null, 2));
    return;
  }

  if (scored.length === 0) {
    console.log("No matching defect-fix cases found.");
    return;
  }
  console.log("Internal defect-fix cases. Do not cite private case ids in public PRs or issue comments.");
  for (const { row, score } of scored) {
    console.log(`\n${row.id}  score=${score}`);
    console.log(`  title: ${row.signals?.[0] || row.id}`);
    console.log(`  modules: ${(row.modules || []).join(", ") || "-"}`);
    console.log(`  files: ${(row.files || []).join(", ") || "-"}`);
    console.log(`  path: ${row.path}`);
  }
}

function recordTrial(wikiRoot, args) {
  const issue = String(args.issue || "").trim();
  const result = String(args.result || "").trim();
  const query = String(args.query || args.text || "").trim();
  if (!issue) throw new Error(`trial requires --issue`);
  if (!["hit", "no_hit"].includes(result)) {
    throw new Error(`trial requires --result hit|no_hit`);
  }
  if (!query) throw new Error(`trial requires --query`);
  const cases = asArray(args.case || args.cases)
    .flatMap((value) => String(value).split(","))
    .map((value) => value.trim())
    .filter(Boolean);
  const record = {
    recorded_at: new Date().toISOString(),
    issue,
    result,
    query,
    cases,
    notes: String(args.notes || "").trim(),
  };
  const trialsPath = join(wikiRoot, "trials/query-before-fix.jsonl");
  const existing = existsSync(trialsPath) ? readFileSync(trialsPath, "utf8") : "";
  writeText(trialsPath, existing + JSON.stringify(record) + "\n");
  console.log(`Recorded ${result} trial for ${issue}`);
}

function printStatus(wikiRoot, args) {
  const sourceCount = walkFiles(join(wikiRoot, "sources/prs")).filter((file) => file.endsWith(".yaml")).length;
  const candidateCount = walkFiles(join(wikiRoot, "candidates")).filter((file) => file.endsWith(".candidate.md")).length;
  const officialCases = loadCasePages(wikiRoot);
  const indexPath = join(wikiRoot, "indexes/cases.index.jsonl");
  const tagsPath = join(wikiRoot, "indexes/tags.index.json");
  const lintPath = join(wikiRoot, "reports/lint-report.json");
  const ingestionPath = join(wikiRoot, "reports/ingestion-report.json");
  const trialRows = loadTrialRows(wikiRoot);
  const trialIssues = new Set(trialRows.map((row) => row.issue));
  const officialMediumPlus = officialCases.filter((page) => ["medium", "high"].includes(page.frontmatter.confidence));
  const status = {
    source_records: sourceCount,
    candidate_drafts: candidateCount,
    official_cases: officialCases.length,
    official_medium_plus_cases: officialMediumPlus.length,
    trial_records: trialRows.length,
    trial_issues: trialIssues.size,
    indexes_present: existsSync(indexPath) && existsSync(tagsPath),
    lint_last_ok: readJsonIfExists(lintPath)?.ok === true,
    last_ingestion: readJsonIfExists(ingestionPath),
    requirements: {
      source_records_20: sourceCount >= 20,
      official_cases_10: officialMediumPlus.length >= 10,
      trial_issues_3: trialIssues.size >= 3,
      indexes_present: existsSync(indexPath) && existsSync(tagsPath),
      lint_ok: readJsonIfExists(lintPath)?.ok === true,
    },
  };
  const complete = Object.values(status.requirements).every(Boolean);
  status.mvp_complete = complete;
  if (args.json) {
    console.log(JSON.stringify(status, null, 2));
    return;
  }
  console.log(`Defect-fix wiki MVP status`);
  console.log(`- source records: ${sourceCount} / 20`);
  console.log(`- candidate drafts: ${candidateCount}`);
  console.log(`- official medium+ cases: ${officialMediumPlus.length} / 10`);
  console.log(`- query-before-fix trial issues: ${trialIssues.size} / 3`);
  console.log(`- indexes present: ${status.requirements.indexes_present ? "yes" : "no"}`);
  console.log(`- last lint ok: ${status.requirements.lint_ok ? "yes" : "no"}`);
  console.log(`- MVP complete: ${complete ? "yes" : "no"}`);
}

function scoreCase(row, query) {
  let score = 0;
  const haystack = [
    row.id,
    row.kind,
    row.status,
    row.confidence,
    ...(row.modules || []),
    ...(row.tags || []),
    ...(row.signals || []),
    ...(row.files || []),
  ].join(" ").toLowerCase();
  for (const token of tokenize(query.text)) {
    if (haystack.includes(token)) score += 2;
  }
  for (const moduleName of query.modules) {
    if ((row.modules || []).includes(moduleName)) score += 4;
  }
  for (const file of query.files) {
    if ((row.files || []).some((caseFile) => caseFile.includes(file) || file.includes(caseFile))) score += 5;
  }
  if (row.status === "active") score += 1;
  if (row.confidence === "high") score += 1;
  return score;
}

function buildIndexSnapshot(wikiRoot, cases) {
  const indexLines = [];
  const tags = {};
  for (const page of cases) {
    const fm = page.frontmatter;
    const row = {
      id: fm.id,
      kind: fm.kind,
      status: fm.status,
      confidence: fm.confidence,
      repo: fm.repo,
      modules: asArray(fm.modules),
      tags: asArray(fm.tags),
      signals: asArray(fm.signals),
      files: asArray(fm.code_refs).map((ref) => ref.path).filter(Boolean),
      source_pr_number: fm.source_pr_number,
      last_reviewed_at: fm.last_reviewed_at || "",
      path: relative(wikiRoot, page.path),
    };
    indexLines.push(JSON.stringify(row));
    for (const tag of row.tags) {
      if (!tags[tag]) tags[tag] = [];
      tags[tag].push(fm.id);
    }
  }
  indexLines.sort();
  const sortedTags = Object.fromEntries(
    Object.entries(tags)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([tag, ids]) => [tag, ids.sort()]),
  );
  return {
    casesIndex: indexLines.join("\n") + (indexLines.length > 0 ? "\n" : ""),
    tagsIndex: JSON.stringify(sortedTags, null, 2) + "\n",
  };
}

function loadCasePages(wikiRoot) {
  const files = [];
  for (const kind of wikiKinds) {
    const dir = join(wikiRoot, "wiki", kind);
    for (const file of walkFiles(dir)) {
      if (file.endsWith(".md")) files.push(file);
    }
  }
  return files.map((path) => {
    const content = readFileSync(path, "utf8");
    const frontmatter = parseFrontmatter(content, path);
    return { path, content, frontmatter };
  });
}

function parseFrontmatter(content, path) {
  if (!content.startsWith("---\n")) {
    throw new Error(`${relative(repoRoot, path)}: missing YAML frontmatter`);
  }
  const end = content.indexOf("\n---", 4);
  if (end === -1) {
    throw new Error(`${relative(repoRoot, path)}: unterminated YAML frontmatter`);
  }
  return parseSimpleYaml(content.slice(4, end));
}

function parseSimpleYaml(text) {
  const root = {};
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i += 1) {
    const line = stripComment(lines[i]);
    if (!line.trim()) continue;
    const top = line.match(/^([A-Za-z0-9_]+):(?:\s*(.*))?$/);
    if (!top) continue;
    const key = top[1];
    const rest = top[2] ?? "";
    if (rest !== "") {
      root[key] = parseScalar(rest);
      continue;
    }
    const block = [];
    while (i + 1 < lines.length && /^  /.test(lines[i + 1])) {
      i += 1;
      block.push(lines[i]);
    }
    root[key] = parseYamlBlock(block);
  }
  return root;
}

function parseYamlBlock(lines) {
  const items = [];
  for (let i = 0; i < lines.length; i += 1) {
    const line = stripComment(lines[i]);
    if (!line.trim()) continue;
    const simple = line.match(/^  -\s*(.*)$/);
    if (!simple) continue;
    const value = simple[1];
    const inlinePair = value.match(/^([A-Za-z0-9_]+):\s*(.*)$/);
    if (!inlinePair) {
      items.push(parseScalar(value));
      continue;
    }
    const obj = { [inlinePair[1]]: parseScalar(inlinePair[2]) };
    while (i + 1 < lines.length && /^    [A-Za-z0-9_]+:/.test(lines[i + 1])) {
      i += 1;
      const pair = stripComment(lines[i]).match(/^    ([A-Za-z0-9_]+):\s*(.*)$/);
      if (pair) obj[pair[1]] = parseScalar(pair[2]);
    }
    items.push(obj);
  }
  return items;
}

function parseScalar(value) {
  const trimmed = value.trim();
  if (trimmed === "[]") return [];
  if (trimmed === "true") return true;
  if (trimmed === "false") return false;
  if (trimmed === "null") return null;
  if (/^-?\d+(?:\.\d+)?$/.test(trimmed)) return Number(trimmed);
  if ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'"))) {
    return trimmed.slice(1, -1);
  }
  return trimmed;
}

function stripComment(line) {
  let inSingle = false;
  let inDouble = false;
  for (let i = 0; i < line.length; i += 1) {
    const ch = line[i];
    if (ch === "'" && !inDouble) inSingle = !inSingle;
    if (ch === '"' && !inSingle) inDouble = !inDouble;
    if (ch === "#" && !inSingle && !inDouble && (i === 0 || /\s/.test(line[i - 1]))) {
      return line.slice(0, i);
    }
  }
  return line;
}

function stringifyYaml(value, indent = 0) {
  const pad = " ".repeat(indent);
  if (Array.isArray(value)) {
    if (value.length === 0) return `${pad}[]\n`;
    return value.map((item) => {
      if (item && typeof item === "object" && !Array.isArray(item)) {
        const entries = Object.entries(item);
        if (entries.length === 0) return `${pad}- {}\n`;
        const [firstKey, firstValue] = entries[0];
        let out = "";
        if (isComplexYamlValue(firstValue)) {
          out += `${pad}- ${firstKey}:\n${stringifyYaml(firstValue, indent + 4)}`;
        } else {
          out += `${pad}- ${firstKey}: ${formatYamlScalar(firstValue)}\n`;
        }
        for (const [key, val] of entries.slice(1)) {
          if (isComplexYamlValue(val)) {
            out += `${pad}  ${key}:\n${stringifyYaml(val, indent + 4)}`;
          } else {
            out += `${pad}  ${key}: ${formatYamlScalar(val)}\n`;
          }
        }
        return out;
      }
      return `${pad}- ${formatYamlScalar(item)}\n`;
    }).join("");
  }
  if (value && typeof value === "object") {
    let out = "";
    for (const [key, val] of Object.entries(value)) {
      if (Array.isArray(val) && val.length === 0) {
        out += `${pad}${key}: []\n`;
      } else if (isComplexYamlValue(val)) {
        out += `${pad}${key}:\n${stringifyYaml(val, indent + 2)}`;
      } else {
        out += `${pad}${key}: ${formatYamlScalar(val)}\n`;
      }
    }
    return out;
  }
  return `${formatYamlScalar(value)}\n`;
}

function isComplexYamlValue(value) {
  return value != null && typeof value === "object";
}

function formatYamlScalar(value) {
  if (value == null) return '""';
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  const text = String(value);
  if (text === "") return '""';
  if (/^[A-Za-z0-9_./:@+-]+$/.test(text)) return text;
  return JSON.stringify(text);
}

function collectLinkedIssues(pr) {
  const refs = new Set();
  for (const issue of pr.closingIssuesReferences || []) {
    if (issue.url) refs.add(issue.url);
    else if (issue.number) refs.add(`#${issue.number}`);
  }
  const text = [pr.title, pr.body, pr.headRefName].filter(Boolean).join("\n");
  for (const match of text.matchAll(/\bMUL-\d+\b/g)) refs.add(match[0]);
  for (const match of text.matchAll(/\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)[:\s]+#(\d+)\b/gi)) {
    refs.add(`#${match[1]}`);
  }
  return [...refs].sort();
}

function isTestFile(path) {
  return /(^|\/)(e2e\/.*\.spec\.[jt]sx?|.*(?:\.test|\.spec)\.[jt]sx?|.*_test\.go)$/.test(path);
}

function isDocsOnlyPath(path) {
  return /(^|\/)(docs|apps\/docs)\//.test(path)
    || /\.(md|mdx|txt|png|jpe?g|gif|svg)$/.test(path)
    || path === "README.md"
    || path === "README.zh-CN.md";
}

function deriveModules(paths) {
  const modules = new Set();
  for (const path of paths) {
    const parts = path.split("/");
    if (parts[0] === "server") modules.add("server");
    else if (parts[0] === "packages" && parts[1]) modules.add(`packages/${parts[1]}`);
    else if (parts[0] === "apps" && parts[1]) modules.add(`apps/${parts[1]}`);
    else if (parts[0]) modules.add(parts[0]);
  }
  return [...modules].slice(0, 5);
}

function deriveTags(title, modules) {
  const tags = new Set(modules.map((moduleName) => moduleName.replace("/", "-")));
  for (const token of tokenize(title || "")) {
    if (["runtime", "daemon", "issue", "agent", "workspace", "auth", "query", "desktop", "mobile", "cli"].includes(token)) {
      tags.add(token);
    }
  }
  return [...tags].slice(0, 8);
}

function slugify(text) {
  const slug = text
    .toLowerCase()
    .replace(/\bmul-\d+\b/g, "")
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .replace(/_+/g, "_")
    .slice(0, 64)
    .replace(/^_+|_+$/g, "");
  return slug || "case";
}

function tokenize(text) {
  return String(text).toLowerCase().match(/[a-z0-9_/-]{2,}/g) || [];
}

function getGitHubBlobSha(ghRepo, path, ref) {
  try {
    const result = execFileSync("gh", ["api", `repos/${ghRepo}/contents/${path}?ref=${ref}`, "--jq", ".sha"], {
      cwd: repoRoot,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
      maxBuffer: 1024 * 1024,
    }).trim();
    return result || "";
  } catch {
    return "";
  }
}

function ghJson(args) {
  const raw = execFileSync("gh", args, {
    cwd: repoRoot,
    encoding: "utf8",
    maxBuffer: 20 * 1024 * 1024,
  });
  return JSON.parse(raw);
}

function walkFiles(dir) {
  if (!existsSync(dir)) return [];
  const out = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...walkFiles(path));
    } else if (entry.isFile()) {
      out.push(path);
    }
  }
  return out;
}

function scanForSecrets(wikiRoot) {
  const patterns = [
    ["aws_access_key", /AKIA[0-9A-Z]{16}/],
    ["openai_key", /sk-[A-Za-z0-9_-]{20,}/],
    ["github_token", /(?:ghp|github_pat)_[A-Za-z0-9_]{20,}/],
    ["slack_token", /xox[baprs]-[A-Za-z0-9-]{20,}/],
    ["private_key", /-----BEGIN [A-Z ]*PRIVATE KEY-----/],
    ["generic_secret", /\b(?:access_token|secret_key|api_key|cookie)\s*[:=]\s*["']?[A-Za-z0-9_/+=.-]{16,}/i],
  ];
  const hits = [];
  for (const file of walkFiles(wikiRoot)) {
    if (!/\.(md|yaml|yml|json|jsonl)$/.test(file)) continue;
    const content = readFileSync(file, "utf8");
    for (const [label, pattern] of patterns) {
      if (pattern.test(content)) hits.push({ path: file, label });
    }
  }
  return hits;
}

function loadTrialRows(wikiRoot) {
  const trialsPath = join(wikiRoot, "trials/query-before-fix.jsonl");
  if (!existsSync(trialsPath)) return [];
  return readFileSync(trialsPath, "utf8")
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line));
}

function readJsonIfExists(path) {
  if (!existsSync(path)) return null;
  return JSON.parse(readFileSync(path, "utf8"));
}

function asArray(value) {
  if (value == null || value === "") return [];
  if (Array.isArray(value)) return value;
  return [value];
}

function quoteBlock(text) {
  return String(text)
    .split("\n")
    .map((line) => `> ${line}`)
    .join("\n");
}

function writeText(path, content) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, content);
}
