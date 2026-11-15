import { lazy, Suspense } from 'react'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import { DataSourceNotice } from '@/components/DataSourceNotice'
import { FilterChips } from '@/components/FilterChips'
import { Legend } from '@/components/Legend'
import { SidePanel } from '@/components/panel/SidePanel'
import { TimelineBar, usePlayback } from '@/components/TimelineBar'
import { TopBar } from '@/components/TopBar'
import { setMap } from '@/map/bridge'
import { MapView } from '@/map/MapView'
import { useAppStore } from '@/store/app-store'

const StoriesView = lazy(() => import('@/components/stories/StoriesView').then((m) => ({ default: m.StoriesView })))

export default function App() {
  usePlayback()
  const panelOpen = useAppStore((s) => s.selection !== null)
  const storyOpen = useAppStore((s) => s.storyId !== null)

  return (
    <TooltipProvider>
      <main className="relative h-full w-full overflow-hidden bg-space text-foreground">
        <MapView onMapReady={setMap} />

        <div className="pointer-events-none absolute inset-0 flex flex-col gap-3 p-3 pb-7 md:p-4 md:pb-7">
          <div className={`flex flex-col gap-3 transition-[margin] ${panelOpen ? 'md:mr-[416px]' : ''}`}>
            <TopBar />
            <div className="pointer-events-auto self-start">
              <FilterChips />
            </div>
          </div>
          <div className="flex-1" />
          <div className={`flex flex-col gap-3 transition-[margin] ${panelOpen ? 'hidden md:mr-[416px] md:flex' : ''}`}>
            <div className="flex flex-col items-start gap-2">
              <DataSourceNotice />
              <div className="pointer-events-auto hidden sm:block">
                <Legend />
              </div>
            </div>
            <div className="pointer-events-auto">
              <TimelineBar />
            </div>
          </div>
        </div>

        <SidePanel />
        {storyOpen && (
          <Suspense fallback={<div className="fixed inset-0 z-40 bg-space" />}>
            <StoriesView />
          </Suspense>
        )}
        <Toaster position="top-center" />
      </main>
    </TooltipProvider>
  )
}
