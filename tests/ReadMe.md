# How to use (examples)

* Create venv + upgrade pip:

  <pre class="overflow-visible!" data-start="2269" data-end="2290"><div class="contain-inline-size rounded-2xl relative bg-token-sidebar-surface-primary"><div class="sticky top-9"><div class="absolute end-0 bottom-0 flex h-9 items-center pe-2"><div class="bg-token-bg-elevated-secondary text-token-text-secondary flex items-center gap-4 rounded-sm px-2 font-sans text-xs"></div></div></div><div class="overflow-y-auto p-4" dir="ltr"><code class="whitespace-pre!"><span><span>make</span><span> venv
  </span></span></code></div></div></pre>
* Compile your `requirements.in` into a pinned `requirements.txt`:

  <pre class="overflow-visible!" data-start="2360" data-end="2384"><div class="contain-inline-size rounded-2xl relative bg-token-sidebar-surface-primary"><div class="sticky top-9"><div class="absolute end-0 bottom-0 flex h-9 items-center pe-2"><div class="bg-token-bg-elevated-secondary text-token-text-secondary flex items-center gap-4 rounded-sm px-2 font-sans text-xs"></div></div></div><div class="overflow-y-auto p-4" dir="ltr"><code class="whitespace-pre!"><span><span>make</span><span> compile
  </span></span></code></div></div></pre>
* Install pinned dependencies (what CI should do):

  <pre class="overflow-visible!" data-start="2438" data-end="2462"><div class="contain-inline-size rounded-2xl relative bg-token-sidebar-surface-primary"><div class="sticky top-9"><div class="absolute end-0 bottom-0 flex h-9 items-center pe-2"><div class="bg-token-bg-elevated-secondary text-token-text-secondary flex items-center gap-4 rounded-sm px-2 font-sans text-xs"></div></div></div><div class="overflow-y-auto p-4" dir="ltr"><code class="whitespace-pre!"><span><span>make</span><span> install
  </span></span></code></div></div></pre>
* Add a new top-level dependency and recompile lock:

  <pre class="overflow-visible!" data-start="2518" data-end="2559"><div class="contain-inline-size rounded-2xl relative bg-token-sidebar-surface-primary"><div class="sticky top-9"><div class="absolute end-0 bottom-0 flex h-9 items-center pe-2"><div class="bg-token-bg-elevated-secondary text-token-text-secondary flex items-center gap-4 rounded-sm px-2 font-sans text-xs"></div></div></div><div class="overflow-y-auto p-4" dir="ltr"><code class="whitespace-pre!"><span><span>make </span><span>add</span><span> PKG=</span><span>"requests>=2.32"</span><span>
  </span></span></code></div></div></pre>

  (This appends to `requirements.in` then recompiles. You can also edit `requirements.in` manually if you prefer.)
* Upgrade a single package safely:

  <pre class="overflow-visible!" data-start="2712" data-end="2749"><div class="contain-inline-size rounded-2xl relative bg-token-sidebar-surface-primary"><div class="sticky top-9"><div class="absolute end-0 bottom-0 flex h-9 items-center pe-2"><div class="bg-token-bg-elevated-secondary text-token-text-secondary flex items-center gap-4 rounded-sm px-2 font-sans text-xs"></div></div></div><div class="overflow-y-auto p-4" dir="ltr"><code class="whitespace-pre!"><span><span>make</span><span> upgrade PKG=requests
  </span></span></code></div></div></pre>
* Make your local venv match the lockfile exactly:

  <pre class="overflow-visible!" data-start="2803" data-end="2824"><div class="contain-inline-size rounded-2xl relative bg-token-sidebar-surface-primary"><div class="sticky top-9"><div class="absolute end-0 bottom-0 flex h-9 items-center pe-2"><div class="bg-token-bg-elevated-secondary text-token-text-secondary flex items-center gap-4 rounded-sm px-2 font-sans text-xs"></div></div></div><div class="overflow-y-auto p-4" dir="ltr"><code class="whitespace-pre!"><span><span>make </span><span>sync</span><span>
  </span></span></code></div></div></pre>
* Clean (remove) the venv:

  <pre class="overflow-visible!" data-start="2854" data-end="2876"><div class="contain-inline-size rounded-2xl relative bg-token-sidebar-surface-primary"><div class="sticky top-9"><div class="absolute end-0 bottom-0 flex h-9 items-center pe-2"><div class="bg-token-bg-elevated-secondary text-token-text-secondary flex items-center gap-4 rounded-sm px-2 font-sans text-xs"></div></div></div><div class="overflow-y-auto p-4" dir="ltr"><code class="whitespace-pre!"><span><span>make</span><span> clean</span></span></code></div></div></pre>
