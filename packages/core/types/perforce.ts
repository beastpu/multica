/** Raw Swarm review state. Treated as an open string at the boundary so a new
 * Swarm value downgrades to a generic rendering rather than crashing. */
export type PerforceReviewState =
  | "needsReview"
  | "needsRevision"
  | "approved"
  | "rejected"
  | "archived";

export interface PerforceConnection {
  workspace_id: string;
  /** The Swarm URL the webhook routes on. v1 is webhook-push only — no
   * credentials are stored. */
  swarm_url: string;
}

export interface GetPerforceConnectionResponse {
  connection: PerforceConnection | null;
  /** Whether the deployment has the webhook token (MULTICA_P4_SWARM_WEBHOOK_TOKEN)
   * set, i.e. whether inbound Swarm pushes can be authenticated. */
  configured: boolean;
  /** Whether the caller may edit the connection. Older backends omit it; treat
   * absence as false for read-only safety. */
  can_manage?: boolean;
}

export interface PerforceReview {
  review_id: number;
  /** Raw Swarm state; render unknown values via a generic fallback. */
  state: string;
  title: string;
  html_url: string;
  author?: string;
  /** Pending changelist under review. */
  shelved_cl?: number;
  /** Submitted changelist once the review lands (renumbered from shelved_cl). */
  committed_cl?: number;
}
