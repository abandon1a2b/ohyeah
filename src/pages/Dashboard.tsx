import { lazy, Suspense } from "react";
import { DataTable } from "@/components/data-table";
import { SectionCards } from "@/components/section-cards";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import type { Status, SyncRun } from "@/types";

const ChartAreaInteractive = lazy(() =>
  import("@/components/chart-area-interactive").then((module) => ({
    default: module.ChartAreaInteractive,
  })),
);

export function Dashboard({
  status,
  runs,
}: {
  status: Status | null;
  runs: SyncRun[];
}) {
  return (
    <>
      <SectionCards status={status} />
      <div className="px-4 lg:px-6">
        <Suspense
          fallback={
            <Card>
              <CardHeader>
                <CardTitle>同步活动</CardTitle>
              </CardHeader>
              <CardContent>
                <Skeleton className="h-64 w-full" />
              </CardContent>
            </Card>
          }
        >
          <ChartAreaInteractive runs={runs} />
        </Suspense>
      </div>
      <DataTable runs={runs} />
    </>
  );
}
