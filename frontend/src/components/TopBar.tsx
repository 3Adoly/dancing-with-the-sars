import { BookOpen } from 'lucide-react'
import { useMeta } from '@/api/hooks'
import { useAppStore } from '@/store/app-store'
import { SearchBox } from './SearchBox'

export function TopBar() {
  const openStory = useAppStore((s) => s.openStory)
  const storyId = useAppStore((s) => s.storyId)
  const { data: meta } = useMeta()
  const storiesOn = meta?.features.stories !== false
  return (
    <header className="pointer-events-auto flex flex-wrap items-center gap-x-6 gap-y-3">
      <a href="./" className="group shrink-0 leading-none" aria-label="Dancing with the SARs, home">
        <span className="type-display block text-[17px] text-foreground sm:text-[19px]">Dancing with the SARs</span>
        <span className="type-label mt-1 block text-[12px] text-muted-foreground">Natural hazards, seen by radar</span>
      </a>
      <div className="order-3 w-full md:order-none md:w-auto md:flex-1">
        <SearchBox />
      </div>
      {storiesOn && (
      <button
        type="button"
        onClick={() => openStory(storyId ? null : 'index')}
        className="ml-auto flex h-10 items-center gap-2 rounded-lg bg-signal px-4 text-sm font-medium text-[#1A1206] hover:brightness-110 md:ml-0"
      >
        <BookOpen className="size-4" /> <span className="sm:hidden">Stories</span><span className="hidden sm:inline">Radar stories</span>
      </button>
      )}
    </header>
  )
}
