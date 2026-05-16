class AgentMemory < Formula
  desc "Persistent memory layer for AI coding agents"
  homepage "https://github.com/taimufuraiyaa/agent-memory"
  license "LicenseRef-Non-Commercial"
  head "https://github.com/taimufuraiyaa/agent-memory.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", "-trimpath", "-ldflags", "-s -w", "-o", bin/"agent-memory", "./cmd/agent-memory"
  end

  test do
    system "#{bin}/agent-memory", "--help"
  end
end
