"use client";

import type { IssueTakeoverCard } from "@multica/core/api";
import { useTakeoverIssue } from "@multica/core/issues/mutations";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Copy, Hand } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { useT } from "../../i18n";

// TakeoverButton runs the interrupt-and-inherit flow: confirm, call the atomic
// takeover, then show the work scene the agent left behind. The card is shown
// immediately from the mutation response rather than refetched — it is the
// one moment the user is certain to be looking.
export function TakeoverButton({
  issueId,
  iconOnly = false,
}: {
  issueId: string;
  /** Compact form for task rows; the full form labels itself. */
  iconOnly?: boolean;
}) {
  const { t } = useT("issues");
  const takeover = useTakeoverIssue();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [card, setCard] = useState<IssueTakeoverCard | null>(null);
  const [cardOpen, setCardOpen] = useState(false);

  const run = async () => {
    try {
      const response = await takeover.mutateAsync(issueId);
      setConfirmOpen(false);
      setCard(response.takeover ?? null);
      setCardOpen(true);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t(($) => $.takeover.failed),
      );
    }
  };

  return (
    <>
      {iconOnly
        ? (
          <button
            type="button"
            onClick={() => setConfirmOpen(true)}
            disabled={takeover.isPending}
            aria-label={t(($) => $.takeover.action)}
            title={t(($) => $.takeover.action)}
            className="flex items-center justify-center rounded p-1 text-muted-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Hand aria-hidden="true" className="size-3.5" />
          </button>
        )
        : (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={takeover.isPending}
            onClick={() => setConfirmOpen(true)}
          >
            <Hand aria-hidden="true" />
            {t(($) => $.takeover.action)}
          </Button>
        )}

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.takeover.confirm_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.takeover.confirm_body)}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setConfirmOpen(false)}
              disabled={takeover.isPending}
            >
              {t(($) => $.takeover.cancel)}
            </Button>
            <Button onClick={run} disabled={takeover.isPending}>
              {t(($) => $.takeover.confirm)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <TakeoverCardDialog
        card={card}
        open={cardOpen}
        onOpenChange={setCardOpen}
      />
    </>
  );
}

// The work-scene handoff. Read-only facts with copy actions: the runtime the
// agent worked on, the directory the work sits in, and the provider session a
// person on that machine can resume. Absent fields simply do not render — a
// task that died early hands over less, not an error.
export function TakeoverCardDialog({
  card,
  open,
  onOpenChange,
}: {
  card: IssueTakeoverCard | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("issues");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(($) => $.takeover.card_title)}</DialogTitle>
          <DialogDescription>
            {card?.from_agent?.name
              ? t(($) => $.takeover.card_from, { agent: card.from_agent.name })
              : t(($) => $.takeover.card_body)}
          </DialogDescription>
        </DialogHeader>
        {card
          ? (
            <div className="space-y-2 text-sm">
              {card.runtime?.name && (
                <CardRow
                  label={t(($) => $.takeover.runtime)}
                  value={card.runtime.name}
                />
              )}
              {card.work_dir && (
                <CardRow
                  label={t(($) => $.takeover.work_dir)}
                  value={card.work_dir}
                  copyable
                />
              )}
              {card.session_id && (
                <CardRow
                  label={t(($) => $.takeover.session)}
                  value={card.session_id}
                  copyable
                  hint={t(($) => $.takeover.session_hint)}
                />
              )}
            </div>
          )
          : (
            <p className="text-sm text-muted-foreground">
              {t(($) => $.takeover.card_empty)}
            </p>
          )}
      </DialogContent>
    </Dialog>
  );
}

function CardRow({
  label,
  value,
  copyable = false,
  hint,
}: {
  label: string;
  value: string;
  copyable?: boolean;
  hint?: string;
}) {
  const { t } = useT("issues");
  return (
    <div className="space-y-0.5">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <div className="flex items-center gap-1.5">
        <code className="min-w-0 flex-1 truncate rounded bg-muted px-1.5 py-1 text-xs">
          {value}
        </code>
        {copyable && (
          <button
            type="button"
            onClick={() => {
              void navigator.clipboard.writeText(value);
              toast.success(t(($) => $.takeover.copied));
            }}
            aria-label={t(($) => $.takeover.copy)}
            className="flex shrink-0 items-center justify-center rounded p-1 text-muted-foreground transition-colors hover:bg-muted"
          >
            <Copy aria-hidden="true" className="size-3.5" />
          </button>
        )}
      </div>
      {hint && <p className="text-[11px] text-muted-foreground">{hint}</p>}
    </div>
  );
}
