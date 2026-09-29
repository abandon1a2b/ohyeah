import {
  BookOpen,
  Database,
  LayoutDashboard,
  Search,
  Settings2,
} from "lucide-react";
import { Separator } from "@/components/ui/separator";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar";
import { cn } from "@/lib/utils";

export const navigation = [
  { id: "dashboard", label: "总览", icon: LayoutDashboard },
  { id: "search", label: "记忆搜索", icon: Search },
  { id: "sources", label: "数据与同步", icon: BookOpen },
  { id: "config", label: "配置管理", icon: Settings2 },
  { id: "index", label: "索引管理", icon: Database },
] as const;

export type Page = (typeof navigation)[number]["id"];

export function AppSidebar({
  page,
  backendAvailable,
  onNavigate,
  ...props
}: {
  page: Page;
  backendAvailable: boolean;
  onNavigate: (page: Page) => void;
} & React.ComponentProps<typeof Sidebar>) {
  const { isMobile, setOpenMobile } = useSidebar();
  const navigate = (next: Page) => {
    onNavigate(next);
    if (isMobile) setOpenMobile(false);
  };
  return (
    <Sidebar collapsible="offcanvas" {...props}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              size="lg"
              tooltip="ohyeah · 工作记忆"
              onClick={() => navigate("dashboard")}
              aria-label="返回总览"
            >
              <span className="grid size-8 shrink-0 place-items-center rounded-md bg-primary text-xs font-bold text-primary-foreground">
                oh
              </span>
              <span className="min-w-0">
                <span className="block truncate text-sm font-semibold">
                  ohyeah
                </span>
                <span className="block truncate text-xs text-muted-foreground">
                  工作记忆
                </span>
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>工作台</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {navigation.map(({ id, label, icon: Icon }) => (
                <SidebarMenuItem key={id}>
                  <SidebarMenuButton
                    size="lg"
                    type="button"
                    isActive={page === id}
                    tooltip={label}
                    aria-current={page === id ? "page" : undefined}
                    onClick={() => navigate(id)}
                  >
                    <Icon />
                    <span>{label}</span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <Separator className="mb-1" />
        <div
          className="flex h-9 items-center gap-3 px-2 text-xs text-muted-foreground"
          title={backendAvailable ? "服务正常" : "连接中断"}
        >
          <span
            className={cn(
              "size-2 shrink-0 rounded-full",
              backendAvailable ? "bg-emerald-500" : "bg-amber-500",
            )}
          />
          <span>{backendAvailable ? "服务正常" : "连接中断"}</span>
        </div>
      </SidebarFooter>
    </Sidebar>
  );
}
