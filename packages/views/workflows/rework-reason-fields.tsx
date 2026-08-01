import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";

import { useT } from "../i18n";
import type { ReworkReasonDraft } from "./rework-reason";

/**
 * Collects the three answers a rework reason needs. Shared by the two doors
 * back to an earlier activity — an acceptance rejection and a manual rollback —
 * so both produce a reason the executor can act on.
 */
export function ReworkReasonFields({
  idPrefix,
  draft,
  onChange,
}: {
  idPrefix: string;
  draft: ReworkReasonDraft;
  onChange: (next: ReworkReasonDraft) => void;
}) {
  const { t } = useT("workflows");
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        {t(($) => $.workbench.rework_reason_help)}
      </p>
      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-criterion`}>
          {t(($) => $.workbench.rework_criterion)}
        </Label>
        <Input
          id={`${idPrefix}-criterion`}
          value={draft.criterion}
          placeholder={t(($) => $.workbench.rework_criterion_placeholder)}
          onChange={(event) =>
            onChange({ ...draft, criterion: event.target.value })}
          className="min-h-11 sm:min-h-8"
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-gap`}>
          {t(($) => $.workbench.rework_gap)}
        </Label>
        <Textarea
          id={`${idPrefix}-gap`}
          value={draft.gap}
          placeholder={t(($) => $.workbench.rework_gap_placeholder)}
          onChange={(event) => onChange({ ...draft, gap: event.target.value })}
          rows={3}
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-hint`}>
          {t(($) => $.workbench.rework_hint)}
        </Label>
        <Input
          id={`${idPrefix}-hint`}
          value={draft.hint}
          placeholder={t(($) => $.workbench.rework_hint_placeholder)}
          onChange={(event) => onChange({ ...draft, hint: event.target.value })}
          className="min-h-11 sm:min-h-8"
        />
      </div>
    </div>
  );
}
