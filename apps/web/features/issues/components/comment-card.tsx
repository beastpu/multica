"use client";

import { useRef, useState } from "react";
import { ChevronRight, Copy, MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipTrigger, TooltipContent } from "@/components/ui/tooltip";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/ui/collapsible";
import { ActorAvatar } from "@/components/common/actor-avatar";
import { ReactionBar } from "@/components/common/reaction-bar";
import { cn } from "@/lib/utils";
import { useActorName } from "@/features/workspace";
import { timeAgo } from "@/shared/utils";
import { RichTextEditor, type RichTextEditorRef } from "@/components/common/rich-text-editor";
import { ReplyInput } from "./reply-input";
import type { TimelineEntry } from "@/shared/types";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface CommentCardProps {
  issueId: string;
  entry: TimelineEntry;
  allReplies: Map<string, TimelineEntry[]>;
  currentUserId?: string;
  onReply: (parentId: string, content: string, attachmentIds?: string[]) => Promise<void>;
  onEdit: (commentId: string, content: string) => Promise<void>;
  onDelete: (commentId: string) => void;
  onToggleReaction: (commentId: string, emoji: string) => void;
}

// ---------------------------------------------------------------------------
// Single comment row (used for both parent and replies within the same Card)
// ---------------------------------------------------------------------------

function CommentRow({
  entry,
  currentUserId,
  onEdit,
  onDelete,
  onToggleReaction,
}: {
  entry: TimelineEntry;
  currentUserId?: string;
  onEdit: (commentId: string, content: string) => Promise<void>;
  onDelete: (commentId: string) => void;
  onToggleReaction: (commentId: string, emoji: string) => void;
}) {
  const { getActorName } = useActorName();
  const [editing, setEditing] = useState(false);
  const editEditorRef = useRef<RichTextEditorRef>(null);

  const isOwn = entry.actor_type === "member" && entry.actor_id === currentUserId;
  const isTemp = entry.id.startsWith("temp-");

  const startEdit = () => {
    setEditing(true);
  };

  const cancelEdit = () => {
    setEditing(false);
  };

  const saveEdit = async () => {
    const trimmed = editEditorRef.current
      ?.getMarkdown()
      ?.replace(/(\n\s*)+$/, "")
      .trim();
    if (!trimmed) return;
    try {
      await onEdit(entry.id, trimmed);
      setEditing(false);
    } catch {
      toast.error("Failed to update comment");
    }
  };

  const reactions = entry.reactions ?? [];

  return (
    <div className={`py-3${isTemp ? " opacity-60" : ""}`}>
      <div className="flex items-center gap-2.5">
        <ActorAvatar actorType={entry.actor_type} actorId={entry.actor_id} size={24} />
        <span className="text-sm font-medium">
          {getActorName(entry.actor_type, entry.actor_id)}
        </span>
        <Tooltip>
          <TooltipTrigger
            render={
              <span className="text-xs text-muted-foreground cursor-default">
                {timeAgo(entry.created_at)}
              </span>
            }
          />
          <TooltipContent side="top">
            {new Date(entry.created_at).toLocaleString()}
          </TooltipContent>
        </Tooltip>

        {!isTemp && (
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button variant="ghost" size="icon-xs" className="ml-auto text-muted-foreground">
                  <MoreHorizontal className="h-4 w-4" />
                </Button>
              }
            />
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => {
                navigator.clipboard.writeText(entry.content ?? "");
                toast.success("Copied");
              }}>
                <Copy className="h-3.5 w-3.5" />
                Copy
              </DropdownMenuItem>
              {isOwn && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onClick={startEdit}>
                    <Pencil className="h-3.5 w-3.5" />
                    Edit
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onClick={() => onDelete(entry.id)} variant="destructive">
                    <Trash2 className="h-3.5 w-3.5" />
                    Delete
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>

      {editing ? (
        <div
          className="mt-2 pl-8"
          onKeyDown={(e) => { if (e.key === "Escape") cancelEdit(); }}
        >
          <div className="max-h-48 overflow-y-auto rounded-md border border-border px-3 py-2">
            <RichTextEditor
              ref={editEditorRef}
              defaultValue={entry.content ?? ""}
              placeholder="Edit comment..."
              onSubmit={saveEdit}
              debounceMs={100}
            />
          </div>
          <div className="flex gap-2 mt-1.5">
            <Button size="sm" onClick={saveEdit}>Save</Button>
            <Button size="sm" variant="ghost" onClick={cancelEdit}>Cancel</Button>
          </div>
        </div>
      ) : (
        <>
          <div className="mt-1.5 pl-8 text-sm leading-relaxed text-foreground/85">
            <RichTextEditor defaultValue={entry.content ?? ""} editable={false} />
          </div>
          {!isTemp && (
            <ReactionBar
              reactions={reactions}
              currentUserId={currentUserId}
              onToggle={(emoji) => onToggleReaction(entry.id, emoji)}
              className="mt-1.5 pl-8"
            />
          )}
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// CommentCard — One Card per thread (parent + all replies flat inside)
// ---------------------------------------------------------------------------

function CommentCard({
  issueId,
  entry,
  allReplies,
  currentUserId,
  onReply,
  onEdit,
  onDelete,
  onToggleReaction,
}: CommentCardProps) {
  const { getActorName } = useActorName();
  const [open, setOpen] = useState(true);
  const [editing, setEditing] = useState(false);
  const editEditorRef = useRef<RichTextEditorRef>(null);

  const isOwn = entry.actor_type === "member" && entry.actor_id === currentUserId;
  const isTemp = entry.id.startsWith("temp-");

  const startEdit = () => {
    setEditing(true);
  };

  const cancelEdit = () => {
    setEditing(false);
  };

  const saveEdit = async () => {
    const trimmed = editEditorRef.current
      ?.getMarkdown()
      ?.replace(/(\n\s*)+$/, "")
      .trim();
    if (!trimmed) return;
    try {
      await onEdit(entry.id, trimmed);
      setEditing(false);
    } catch {
      toast.error("Failed to update comment");
    }
  };

  // Collect all nested replies recursively into a flat list
  const allNestedReplies: TimelineEntry[] = [];
  const collectReplies = (parentId: string) => {
    const children = allReplies.get(parentId) ?? [];
    for (const child of children) {
      allNestedReplies.push(child);
      collectReplies(child.id);
    }
  };
  collectReplies(entry.id);

  const replyCount = allNestedReplies.length;
  const contentPreview = (entry.content ?? "").replace(/\n/g, " ").slice(0, 80);
  const reactions = entry.reactions ?? [];

  return (
    <Card className={`!py-0 !gap-0 overflow-hidden${isTemp ? " opacity-60" : ""}`}>
      <Collapsible open={open} onOpenChange={setOpen}>
        {/* Header — always visible, acts as toggle */}
        <div className="px-4 py-3">
          <div className="flex items-center gap-2.5">
            <CollapsibleTrigger className="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground transition-colors">
              <ChevronRight className={cn("h-3.5 w-3.5 transition-transform", open && "rotate-90")} />
            </CollapsibleTrigger>
            <ActorAvatar actorType={entry.actor_type} actorId={entry.actor_id} size={24} />
            <span className="shrink-0 text-sm font-medium">
              {getActorName(entry.actor_type, entry.actor_id)}
            </span>
            <Tooltip>
              <TooltipTrigger
                render={
                  <span className="shrink-0 text-xs text-muted-foreground cursor-default">
                    {timeAgo(entry.created_at)}
                  </span>
                }
              />
              <TooltipContent side="top">
                {new Date(entry.created_at).toLocaleString()}
              </TooltipContent>
            </Tooltip>

            {!open && contentPreview && (
              <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
                {contentPreview}
              </span>
            )}
            {!open && replyCount > 0 && (
              <span className="shrink-0 text-xs text-muted-foreground">
                {replyCount} {replyCount === 1 ? "reply" : "replies"}
              </span>
            )}

            {open && !isTemp && (
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button variant="ghost" size="icon-xs" className="ml-auto text-muted-foreground">
                      <MoreHorizontal className="h-4 w-4" />
                    </Button>
                  }
                />
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onClick={() => {
                    navigator.clipboard.writeText(entry.content ?? "");
                    toast.success("Copied");
                  }}>
                    <Copy className="h-3.5 w-3.5" />
                    Copy
                  </DropdownMenuItem>
                  {isOwn && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem onClick={startEdit}>
                        <Pencil className="h-3.5 w-3.5" />
                        Edit
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem onClick={() => onDelete(entry.id)} variant="destructive">
                        <Trash2 className="h-3.5 w-3.5" />
                        Delete
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </div>
        </div>

        {/* Collapsible body */}
        <CollapsibleContent>
          {/* Parent comment body */}
          <div className="px-4 pb-3">
            {editing ? (
              <div
                className="pl-10"
                onKeyDown={(e) => { if (e.key === "Escape") cancelEdit(); }}
              >
                <div className="max-h-48 overflow-y-auto rounded-md border border-border px-3 py-2">
                  <RichTextEditor
                    ref={editEditorRef}
                    defaultValue={entry.content ?? ""}
                    placeholder="Edit comment..."
                    onSubmit={saveEdit}
                    debounceMs={100}
                  />
                </div>
                <div className="flex gap-2 mt-1.5">
                  <Button size="sm" onClick={saveEdit}>Save</Button>
                  <Button size="sm" variant="ghost" onClick={cancelEdit}>Cancel</Button>
                </div>
              </div>
            ) : (
              <>
                <div className="pl-10 text-sm leading-relaxed text-foreground/85">
                  <RichTextEditor defaultValue={entry.content ?? ""} editable={false} />
                </div>
                {!isTemp && (
                  <ReactionBar
                    reactions={reactions}
                    currentUserId={currentUserId}
                    onToggle={(emoji) => onToggleReaction(entry.id, emoji)}
                    className="mt-1.5 pl-10"
                  />
                )}
              </>
            )}
          </div>

          {/* Replies */}
          {allNestedReplies.map((reply) => (
            <div key={reply.id} className="border-t border-border/50 px-4">
              <CommentRow
                entry={reply}
                currentUserId={currentUserId}
                onEdit={onEdit}
                onDelete={onDelete}
                onToggleReaction={onToggleReaction}
              />
            </div>
          ))}

          {/* Reply input */}
          <div className="border-t border-border/50 px-4 py-2.5">
            <ReplyInput
              issueId={issueId}
              placeholder="Leave a reply..."
              size="sm"
              avatarType="member"
              avatarId={currentUserId ?? ""}
              onSubmit={(content, attachmentIds) => onReply(entry.id, content, attachmentIds)}
            />
          </div>
        </CollapsibleContent>
      </Collapsible>
    </Card>
  );
}

export { CommentCard, type CommentCardProps };
