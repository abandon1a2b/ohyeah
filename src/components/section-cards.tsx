import { Activity, BookOpen, FileText, ListFilter } from "lucide-react";
import {
  Card,
  CardAction,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import type { Status } from "@/types";

export function SectionCards({ status }: { status: Status | null }) {
  const cards = [
    {
      label: "记忆单元",
      value: status?.stats.memoryUnits ?? 0,
      icon: FileText,
    },
    { label: "数据来源", value: status?.stats.sources ?? 0, icon: BookOpen },
    { label: "关联关系", value: status?.stats.relations ?? 0, icon: Activity },
    {
      label: "待处理索引",
      value: status?.stats.pendingOutbox ?? 0,
      icon: ListFilter,
    },
  ];

  return (
    <div className="grid grid-cols-2 gap-3 px-4 *:data-[slot=card]:bg-gradient-to-t *:data-[slot=card]:from-primary/5 *:data-[slot=card]:to-card *:data-[slot=card]:shadow-xs lg:grid-cols-4 lg:gap-4 lg:px-6">
      {cards.map(({ label, value, icon: Icon }) => (
        <Card key={label} className="min-w-0 gap-3 py-5">
          <CardHeader className="px-4 lg:px-5">
            <CardDescription>{label}</CardDescription>
            <CardAction>
              <Icon className="size-4 text-muted-foreground" />
            </CardAction>
            <CardTitle className="text-2xl font-semibold tabular-nums lg:text-3xl">
              {value.toLocaleString()}
            </CardTitle>
          </CardHeader>
        </Card>
      ))}
    </div>
  );
}
