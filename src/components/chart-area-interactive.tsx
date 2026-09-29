import { useMemo, useState } from "react";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { SyncRun } from "@/types";

const chartConfig = {
  scanned: { label: "扫描", color: "var(--chart-1)" },
  changed: { label: "变更", color: "var(--chart-2)" },
} satisfies ChartConfig;

export function ChartAreaInteractive({ runs }: { runs: SyncRun[] }) {
  const [range, setRange] = useState("24h");
  const data = useMemo(() => {
    const hourly = range === "24h";
    const count = hourly ? 24 : Number(range.slice(0, -1));
    const now = new Date();
    now.setMinutes(0, 0, 0);
    const buckets = Array.from({ length: count }, (_, offset) => {
      const date = new Date(now);
      if (hourly) date.setHours(now.getHours() - count + offset + 1);
      else date.setDate(now.getDate() - count + offset + 1);
      return {
        date: hourly
          ? `${date.toLocaleDateString("sv-SE")} ${String(date.getHours()).padStart(2, "0")}:00`
          : date.toLocaleDateString("sv-SE"),
        scanned: 0,
        changed: 0,
      };
    });
    const byDate = new Map(buckets.map((item) => [item.date, item]));
    for (const run of runs) {
      const started = new Date(run.startedAt);
      const key = hourly
        ? `${started.toLocaleDateString("sv-SE")} ${String(started.getHours()).padStart(2, "0")}:00`
        : started.toLocaleDateString("sv-SE");
      const bucket = byDate.get(key);
      if (bucket) {
        bucket.scanned += run.scanned;
        bucket.changed += run.changed;
      }
    }
    return buckets;
  }, [runs, range]);

  return (
    <Card className="@container/card gap-2">
      <CardHeader>
        <CardTitle>同步活动</CardTitle>
        <CardDescription>
          最近 {range === "24h" ? "24 小时" : `${range.slice(0, -1)} 天`}
          的扫描与变更记录
        </CardDescription>
        <CardAction>
          <ToggleGroup
            type="single"
            value={range}
            onValueChange={(value) => value && setRange(value)}
            variant="outline"
            className="hidden @md/card:flex"
          >
            <ToggleGroupItem value="24h">24 小时</ToggleGroupItem>
            <ToggleGroupItem value="7d">7 天</ToggleGroupItem>
            <ToggleGroupItem value="30d">30 天</ToggleGroupItem>
          </ToggleGroup>
          <Select value={range} onValueChange={setRange}>
            <SelectTrigger
              className="w-28 @md/card:hidden"
              size="sm"
              aria-label="时间范围"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value="24h">24 小时</SelectItem>
                <SelectItem value="7d">7 天</SelectItem>
                <SelectItem value="30d">30 天</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </CardAction>
      </CardHeader>
      <CardContent className="px-2 pt-3 sm:px-6">
        <ChartContainer
          config={chartConfig}
          className="aspect-auto h-64 w-full"
        >
          <AreaChart data={data} accessibilityLayer>
            <defs>
              <linearGradient id="sync-scanned" x1="0" y1="0" x2="0" y2="1">
                <stop
                  offset="5%"
                  stopColor="var(--color-scanned)"
                  stopOpacity={0.3}
                />
                <stop
                  offset="95%"
                  stopColor="var(--color-scanned)"
                  stopOpacity={0.02}
                />
              </linearGradient>
              <linearGradient id="sync-changed" x1="0" y1="0" x2="0" y2="1">
                <stop
                  offset="5%"
                  stopColor="var(--color-changed)"
                  stopOpacity={0.25}
                />
                <stop
                  offset="95%"
                  stopColor="var(--color-changed)"
                  stopOpacity={0.02}
                />
              </linearGradient>
            </defs>
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="date"
              tickLine={false}
              axisLine={false}
              minTickGap={28}
              tickFormatter={(value) =>
                range === "24h" ? value.slice(11) : value.slice(5)
              }
            />
            <YAxis tickLine={false} axisLine={false} width={34} />
            <ChartTooltip content={<ChartTooltipContent indicator="dot" />} />
            <Area
              dataKey="scanned"
              type="monotone"
              isAnimationActive={false}
              stroke="var(--color-scanned)"
              fill="url(#sync-scanned)"
              strokeWidth={2}
            />
            <Area
              dataKey="changed"
              type="monotone"
              isAnimationActive={false}
              stroke="var(--color-changed)"
              fill="url(#sync-changed)"
              strokeWidth={2}
            />
          </AreaChart>
        </ChartContainer>
      </CardContent>
    </Card>
  );
}
