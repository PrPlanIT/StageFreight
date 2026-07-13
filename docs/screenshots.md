# Screenshots

StageFreight dogfoods itself — everything on this site is built and shipped by StageFreight.
The captures below are **real pipelines**: StageFreight building StageFreight, the same run
rendered natively in both GitLab CI and GitHub Actions, plus a GitOps run. This is what the
structured, phase-by-phase output actually looks like.

## The canonical pipeline

A run moves through five load-bearing phases — **audition → perform → review → publish →
narrate** — the same graph regardless of forge:

![StageFreight canonical pipeline graph in GitLab](assets/screenshots/StageFreight-GitLab-Canonical-Pipeline-Graph-1.png){ loading=lazy }

## Build pipeline, phase by phase

The same StageFreight build, shown in GitLab and GitHub. Each phase renders identical
structured panels — only the CI chrome differs. The numbered captures are consecutive
segments of one (tall) job log.

### 1. Audition — lint, freshness, secret, and hygiene gates

The environment banner, Code/Executor/Config panels, staged toolchain, the lint matrix, and
the findings gate.

=== "GitLab"

    ![Audition job in GitLab, part 1](assets/screenshots/StageFreight-GitLab-Build-Job-1-Audition-1.png){ loading=lazy }

    ![Audition job in GitLab, part 2](assets/screenshots/StageFreight-GitLab-Build-Job-1-Audition-2.png){ loading=lazy }

    ![Audition job in GitLab, part 3](assets/screenshots/StageFreight-GitLab-Build-Job-1-Audition-3.png){ loading=lazy }

=== "GitHub"

    ![Audition job in GitHub Actions, part 1](assets/screenshots/StageFreight-GitHub-Build-Job-1-Audition-1.png){ loading=lazy }

    ![Audition job in GitHub Actions, part 2](assets/screenshots/StageFreight-GitHub-Build-Job-1-Audition-2.png){ loading=lazy }

    ![Audition job in GitHub Actions, part 3](assets/screenshots/StageFreight-GitHub-Build-Job-1-Audition-3.png){ loading=lazy }

### 2. Perform — produce artifacts in containers

Detect → plan → build, with cache retention and the transport archives that carry artifacts
into publish.

=== "GitLab"

    ![Perform job in GitLab, part 1](assets/screenshots/StageFreight-GitLab-Build-Job-2-Perform-1.png){ loading=lazy }

    ![Perform job in GitLab, part 2](assets/screenshots/StageFreight-GitLab-Build-Job-2-Perform-2.png){ loading=lazy }

=== "GitHub"

    ![Perform job in GitHub Actions](assets/screenshots/StageFreight-GitHub-Build-Job-2-Perform-1.png){ loading=lazy }

### 3. Review — inspect produced artifacts before publish

=== "GitLab"

    ![Review job in GitLab, part 1](assets/screenshots/StageFreight-GitLab-Build-Job-3-Review-1.png){ loading=lazy }

    ![Review job in GitLab, part 2](assets/screenshots/StageFreight-GitLab-Build-Job-3-Review-2.png){ loading=lazy }

    ![Review job in GitLab, part 3](assets/screenshots/StageFreight-GitLab-Build-Job-3-Review-3.png){ loading=lazy }

=== "GitHub"

    ![Review job in GitHub Actions, part 1](assets/screenshots/StageFreight-GitHub-Build-Job-3-Review-1.png){ loading=lazy }

    ![Review job in GitHub Actions, part 3](assets/screenshots/StageFreight-GitHub-Build-Job-3-Review-3.png){ loading=lazy }

### 4. Publish — push images, cut releases, deploy, retain

=== "GitLab"

    ![Publish job in GitLab](assets/screenshots/StageFreight-GitLab-Build-Job-4-Publish-1.png){ loading=lazy }

=== "GitHub"

    ![Publish job in GitHub Actions](assets/screenshots/StageFreight-GitHub-Build-Job-4-Publish-1.png){ loading=lazy }

### 5. Narrate — compose repo-facing content and commit it

=== "GitLab"

    ![Narrate job in GitLab](assets/screenshots/StageFreight-GitLab-Build-Job-5-Narrate-1.png){ loading=lazy }

=== "GitHub"

    ![Narrate job in GitHub Actions](assets/screenshots/StageFreight-GitHub-Build-Job-5-Narrate-1.png){ loading=lazy }

## GitOps pipeline

StageFreight also drives GitOps runs. Here the **audition** phase adds Kustomize/GitOps
validation (schema and authority checks across the tree) before **perform**:

### Audition — GitOps validation

![GitOps audition job in GitLab](assets/screenshots/StageFreight-GitLab-GitOps-Job-1-Audition-1.png){ loading=lazy }

### Perform

![GitOps perform job in GitLab](assets/screenshots/StageFreight-GitLab-GitOps-Job-2-Perform-1.png){ loading=lazy }
