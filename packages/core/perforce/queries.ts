import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const perforceKeys = {
  all: (wsId: string) => ["perforce", wsId] as const,
  connection: (wsId: string) => [...perforceKeys.all(wsId), "connection"] as const,
  reviews: (issueId: string) => ["perforce", "reviews", issueId] as const,
};

export const perforceConnectionOptions = (wsId: string) =>
  queryOptions({
    queryKey: perforceKeys.connection(wsId),
    queryFn: () => api.getPerforceConnection(wsId),
    enabled: !!wsId,
  });

export const issuePerforceReviewsOptions = (issueId: string) =>
  queryOptions({
    queryKey: perforceKeys.reviews(issueId),
    queryFn: () => api.listIssuePerforceReviews(issueId),
    enabled: !!issueId,
  });
