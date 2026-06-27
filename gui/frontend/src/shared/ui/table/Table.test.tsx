import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Table, TableBody, TableCell, TableHead, TableHeaderCell, TableRow } from "./Table";

describe("Table", () => {
  it("renders header and body cells", () => {
    render(
      <Table>
        <TableHead>
          <TableRow>
            <TableHeaderCell>PID</TableHeaderCell>
          </TableRow>
        </TableHead>
        <TableBody>
          <TableRow>
            <TableCell>123</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    );
    expect(screen.getByRole("columnheader", { name: "PID" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "123" })).toBeInTheDocument();
  });
});
